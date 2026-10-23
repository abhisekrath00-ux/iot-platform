package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/rules"
)

// Polygon zones: create/update validate and store the polygon, getMap returns
// it and counts membership by the polygon (not the display frame circle), and
// the enter/exit evaluator uses the same shape.
func TestIntegrationGeoMapPolygon(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-gp")
	ctx := context.Background()
	clean := func() {
		s.st.Pool.Exec(ctx, `DELETE FROM alerts WHERE tenant_id='itest-gp'`)
		s.st.Pool.Exec(ctx, `DELETE FROM geofence_state WHERE tenant_id='itest-gp'`)
		s.st.Pool.Exec(ctx, `DELETE FROM geofences WHERE tenant_id='itest-gp'`)
		s.st.Pool.Exec(ctx, `UPDATE devices SET lat=NULL, lon=NULL WHERE tenant_id='itest-gp'`)
		s.st.Pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id='itest-gp'`)
	}
	clean()
	t.Cleanup(clean)
	api := http.NewServeMux()
	api.HandleFunc("PUT /v1/devices/{id}/location", s.setDeviceLocation)
	api.HandleFunc("GET /v1/map", s.getMap)
	api.HandleFunc("POST /v1/geofences", s.createGeofence)
	api.HandleFunc("PUT /v1/geofences/{id}", s.updateGeofence)

	// An L-shaped zone with a notch around (12.9, 77.6). The notch is the
	// telling shape: the display frame covers it, the polygon does not.
	const notchZone = `{"name":"Yard L","polygon":[[12.898,77.598],[12.898,77.602],[12.902,77.602],[12.902,77.598],[12.9005,77.598],[12.9005,77.6013],[12.8995,77.6013],[12.8995,77.598]],"alert_on_enter":true,"alert_on_exit":true,"alert_severity":"critical"}`

	// Malformed polygons are rejected, not guessed.
	for _, bad := range []string{
		`{"name":"x","polygon":[[12.9,77.6],[12.91,77.61]]}`,         // too few vertices
		`{"name":"x","polygon":[[12.9],[12.91,77.61],[12.9,77.62]]}`, // vertex not a pair
		`{"name":"x","polygon":[[86,0],[0,1],[0,-1]]}`,               // past 85 latitude
		`{"name":"x","polygon":[[0,179],[1,-179],[0,178]]}`,          // crosses the antimeridian
	} {
		if w := call(api, "itest-gp", "operator", "POST", "/v1/geofences", bad); w.Code != 400 {
			t.Fatalf("accepted %s: %d %s", bad, w.Code, w.Body.String())
		}
	}

	// Create the L zone; lat/lon/radius in the body are replaced by the frame.
	w := call(api, "itest-gp", "operator", "POST", "/v1/geofences", notchZone)
	if w.Code != 201 {
		t.Fatalf("create polygon zone: %d %s", w.Code, w.Body.String())
	}
	var created struct{ ID string }
	json.Unmarshal(w.Body.Bytes(), &created)
	var storedLat, storedLon, storedR float64
	var polyLen int
	if err := s.st.Pool.QueryRow(ctx, `SELECT lat, lon, radius_m, jsonb_array_length(polygon) FROM geofences WHERE id=$1`, created.ID).Scan(&storedLat, &storedLon, &storedR, &polyLen); err != nil {
		t.Fatal(err)
	}
	if polyLen != 8 || storedLat < 12.899 || storedLat > 12.901 || storedR < 200 || storedR > 500 {
		t.Fatalf("stored polygon=%d vertices frame=%v,%v r=%v", polyLen, storedLat, storedLon, storedR)
	}

	// getMap returns the polygon and counts membership by shape: a device in
	// the notch is metres from the frame centre (inside the frame circle) but
	// outside the L, and must read as outside.
	if call(api, "itest-gp", "operator", "PUT", "/v1/devices/itest-gp-dev/location", `{"lat":12.9,"lon":77.6}`).Code != 200 {
		t.Fatal("set location")
	}
	var m struct {
		Devices []struct{ Inside []string }
	}
	json.Unmarshal(call(api, "itest-gp", "viewer", "GET", "/v1/map", "").Body.Bytes(), &m)
	if len(m.Devices) != 1 || len(m.Devices[0].Inside) != 0 {
		t.Fatalf("device in the notch counted inside: %+v", m.Devices)
	}
	// A solid corner of the L is inside.
	call(api, "itest-gp", "operator", "PUT", "/v1/devices/itest-gp-dev/location", `{"lat":12.8985,"lon":77.6015}`)
	json.Unmarshal(call(api, "itest-gp", "viewer", "GET", "/v1/map", "").Body.Bytes(), &m)
	if len(m.Devices[0].Inside) != 1 || m.Devices[0].Inside[0] != "Yard L" {
		t.Fatalf("solid corner not inside: %+v", m.Devices)
	}

	// The evaluator raises enter/exit alerts on the polygon too. Baseline
	// first, with the device back in the notch (outside), recorded silently.
	call(api, "itest-gp", "operator", "PUT", "/v1/devices/itest-gp-dev/location", `{"lat":12.9,"lon":77.6}`)
	rules.EvaluateGeofences(ctx, s.st.Pool, nil)
	count := func() (n int) {
		s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE device_id='itest-gp-dev' AND geofence_id=$1`, created.ID).Scan(&n)
		return
	}
	if n := count(); n != 0 {
		t.Fatalf("baseline raised %d alerts, want 0", n)
	}
	call(api, "itest-gp", "operator", "PUT", "/v1/devices/itest-gp-dev/location", `{"lat":12.8987,"lon":77.6014}`)
	rules.EvaluateGeofences(ctx, s.st.Pool, nil)
	if n := count(); n != 1 {
		t.Fatalf("enter raised %d alerts, want 1", n)
	}
	var msg string
	s.st.Pool.QueryRow(ctx, `SELECT message FROM alerts WHERE device_id='itest-gp-dev' AND geofence_id=$1`, created.ID).Scan(&msg)
	if want := `Boiler PLC entered zone "Yard L"`; msg != want {
		t.Fatalf("message %q, want %q", msg, want)
	}
	call(api, "itest-gp", "operator", "PUT", "/v1/devices/itest-gp-dev/location", `{"lat":12.9,"lon":77.6}`)
	rules.EvaluateGeofences(ctx, s.st.Pool, nil)
	if n := count(); n != 2 {
		t.Fatalf("exit raised %d alerts, want 2", n)
	}

	// Update semantics: a body without polygon turns the zone back into a
	// circle and clears the stored polygon.
	if w := call(api, "itest-gp", "operator", "PUT", "/v1/geofences/"+created.ID, `{"name":"Yard L","lat":12.9,"lon":77.6,"radius_m":300,"alert_severity":"warning"}`); w.Code != 204 {
		t.Fatalf("update to circle: %d %s", w.Code, w.Body.String())
	}
	var isNull bool
	if err := s.st.Pool.QueryRow(ctx, `SELECT polygon IS NULL FROM geofences WHERE id=$1`, created.ID).Scan(&isNull); err != nil || !isNull {
		t.Fatalf("polygon not cleared: %v %v", isNull, err)
	}
	// ...and a circle can become a polygon on update.
	if w := call(api, "itest-gp", "operator", "PUT", "/v1/geofences/"+created.ID, `{"name":"Yard L","polygon":[[12.898,77.598],[12.898,77.602],[12.902,77.602],[12.902,77.598]],"alert_severity":"warning"}`); w.Code != 204 {
		t.Fatalf("update to polygon: %d %s", w.Code, w.Body.String())
	}
	if err := s.st.Pool.QueryRow(ctx, `SELECT jsonb_array_length(polygon) FROM geofences WHERE id=$1`, created.ID).Scan(&polyLen); err != nil || polyLen != 4 {
		t.Fatalf("polygon not set on update: %d %v", polyLen, err)
	}
}
