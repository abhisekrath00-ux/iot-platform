package main

import (
	"encoding/json"
	"math"
	"net/http"
	"testing"
)

func TestHaversine(t *testing.T) {
	// one degree of latitude is about 111.2 km
	if d := haversine(0, 0, 1, 0); math.Abs(d-111195) > 200 {
		t.Fatalf("1 degree = %v m", d)
	}
	if haversine(12.9, 77.6, 12.9, 77.6) != 0 {
		t.Fatal("same point")
	}
}

func TestIntegrationGeoMap(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-gm")
	seed(t, s, "itest-gm2")
	ctx := t.Context()
	clean := func() {
		s.st.Pool.Exec(ctx, `DELETE FROM geofences WHERE tenant_id IN ('itest-gm','itest-gm2')`)
		s.st.Pool.Exec(ctx, `UPDATE devices SET lat=NULL, lon=NULL WHERE tenant_id IN ('itest-gm','itest-gm2')`)
		s.st.Pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id IN ('itest-gm','itest-gm2')`)
	}
	clean()
	t.Cleanup(clean)
	api := http.NewServeMux()
	api.HandleFunc("PUT /v1/devices/{id}/location", s.setDeviceLocation)
	api.HandleFunc("GET /v1/map", s.getMap)
	api.HandleFunc("POST /v1/geofences", s.createGeofence)
	api.HandleFunc("DELETE /v1/geofences/{id}", s.deleteGeofence)
	if w := call(api, "itest-gm", "operator", "POST", "/v1/geofences", `{"name":"Plant","lat":12.9,"lon":77.6,"radius_m":500}`); w.Code != 201 {
		t.Fatalf("fence %d", w.Code)
	}
	for _, bad := range []string{`{"name":"x","lat":91,"lon":0,"radius_m":5}`, `{"name":"x","lat":1,"lon":1,"radius_m":0}`, `{"name":"<b>","lat":1,"lon":1,"radius_m":5}`} {
		if call(api, "itest-gm", "operator", "POST", "/v1/geofences", bad).Code != 400 {
			t.Fatalf("accepted %s", bad)
		}
	}
	if call(api, "itest-gm", "viewer", "POST", "/v1/geofences", `{"name":"x","lat":1,"lon":1,"radius_m":5}`).Code != 403 {
		t.Fatal("viewer created a fence")
	}
	// the seeded device is about 200 m from the plant centre
	if call(api, "itest-gm", "operator", "PUT", "/v1/devices/itest-gm-dev/location", `{"lat":12.9018,"lon":77.6}`).Code != 200 {
		t.Fatal("set location")
	}
	if call(api, "itest-gm", "operator", "PUT", "/v1/devices/itest-gm-dev/location", `{"lat":12.9}`).Code != 400 || call(api, "itest-gm", "operator", "PUT", "/v1/devices/itest-gm-dev/location", `{"lat":99,"lon":0}`).Code != 400 {
		t.Fatal("bad location accepted")
	}
	if call(api, "itest-gm2", "operator", "PUT", "/v1/devices/itest-gm-dev/location", `{"lat":1,"lon":1}`).Code != 404 {
		t.Fatal("another tenant moved the device")
	}
	var m struct {
		Devices []struct {
			ID     string
			Inside []string
		}
		Geofences []struct{ ID string }
	}
	w := call(api, "itest-gm", "viewer", "GET", "/v1/map", "")
	json.Unmarshal(w.Body.Bytes(), &m)
	if len(m.Devices) != 1 || len(m.Devices[0].Inside) != 1 || m.Devices[0].Inside[0] != "Plant" {
		t.Fatalf("inside: %s", w.Body.String())
	}
	// move it 2 km away: outside
	call(api, "itest-gm", "operator", "PUT", "/v1/devices/itest-gm-dev/location", `{"lat":12.92,"lon":77.6}`)
	w = call(api, "itest-gm", "viewer", "GET", "/v1/map", "")
	json.Unmarshal(w.Body.Bytes(), &m)
	if len(m.Devices[0].Inside) != 0 {
		t.Fatalf("should be outside: %s", w.Body.String())
	}
	var o struct{ Devices []any }
	json.Unmarshal(call(api, "itest-gm2", "viewer", "GET", "/v1/map", "").Body.Bytes(), &o)
	if len(o.Devices) != 0 {
		t.Fatal("another tenant sees the device")
	}
	if call(api, "itest-gm", "operator", "DELETE", "/v1/geofences/"+m.Geofences[0].ID, "").Code != 204 {
		t.Fatal("delete fence")
	}
}
