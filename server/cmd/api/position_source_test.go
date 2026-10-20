package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
)

// Live position from telemetry: a device names the two points that carry its
// latitude and longitude; ingest then keeps devices.lat/lon current, so the
// map and geofence alerts follow reported positions.
func TestIntegrationLocationSource(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-ps1")
	s.pos = store.NewPositionTracker(s.st.Pool)
	ctx := context.Background()
	pool := s.st.Pool
	dev := "itest-ps1-dev"
	for _, q := range []string{
		`INSERT INTO points(id,device_id,unit) VALUES('lat','itest-ps1-dev','deg'),('lon','itest-ps1-dev','deg')`,
		`DELETE FROM audit_log WHERE tenant_id='itest-ps1'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM telemetry WHERE tenant_id='itest-ps1'`)
		pool.Exec(ctx, `DELETE FROM devices WHERE tenant_id='itest-ps1'`)
	})

	api := http.NewServeMux()
	api.HandleFunc("PUT /v1/devices/{id}/location-source", s.setDeviceLocationSource)
	api.HandleFunc("POST /v1/telemetry/ingest", s.ingestHTTP)

	putSrc := func(body string) int {
		return call(api, "itest-ps1", "operator", "PUT", "/v1/devices/"+dev+"/location-source", body).Code
	}
	// validation: one-sided, equal, unknown point, unknown device
	if c := putSrc(`{"lat_point":"lat"}`); c != 400 {
		t.Fatalf("one-sided got %d", c)
	}
	if c := putSrc(`{"lat_point":"lat","lon_point":"lat"}`); c != 400 {
		t.Fatalf("same point twice got %d", c)
	}
	if c := putSrc(`{"lat_point":"lat","lon_point":"nope"}`); c != 404 {
		t.Fatalf("unknown point got %d", c)
	}
	if c := call(api, "itest-ps1", "operator", "PUT", "/v1/devices/itest-ps1-ghost/location-source", `{"lat_point":"lat","lon_point":"lon"}`).Code; c != 404 {
		t.Fatalf("unknown device got %d", c)
	}
	if c := putSrc(`{"lat_point":"lat","lon_point":"lon"}`); c != 200 {
		t.Fatalf("valid config got %d", c)
	}

	post := func(body string) int {
		return call(api, "itest-ps1", "operator", "POST", "/v1/telemetry/ingest", body).Code
	}
	pos := func() (lat, lon *float64) {
		if err := pool.QueryRow(ctx, `SELECT lat, lon FROM devices WHERE id=$1`, dev).Scan(&lat, &lon); err != nil {
			t.Fatal(err)
		}
		return
	}

	// a batch carrying both points fixes the position
	if c := post(`{"device_id":"` + dev + `","readings":[{"point":"lat","value":12.5},{"point":"lon","value":77.25}]}`); c != 200 {
		t.Fatalf("position batch got %d", c)
	}
	if lat, lon := pos(); lat == nil || lon == nil || *lat != 12.5 || *lon != 77.25 {
		t.Fatalf("position = %v,%v", lat, lon)
	}
	// a later single-point reading moves one axis, keeps the other
	post(`{"device_id":"` + dev + `","readings":[{"point":"lat","value":12.6}]}`)
	if lat, lon := pos(); lat == nil || *lat != 12.6 || lon == nil || *lon != 77.25 {
		t.Fatalf("after lat move = %v,%v", lat, lon)
	}
	// out-of-range coordinate is dropped, position untouched
	post(`{"device_id":"` + dev + `","readings":[{"point":"lat","value":95}]}`)
	if lat, _ := pos(); lat == nil || *lat != 12.6 {
		t.Fatalf("invalid lat overwrote position: %v", lat)
	}
	// an unrelated point never moves the device
	post(`{"device_id":"` + dev + `","readings":[{"point":"temp","value":50}]}`)
	if lat, lon := pos(); lat == nil || *lat != 12.6 || lon == nil || *lon != 77.25 {
		t.Fatalf("unrelated point moved position: %v,%v", lat, lon)
	}

	// config reload: after the cache is reset, a changed source takes effect
	if _, err := pool.Exec(ctx, `UPDATE devices SET lat_point='lon', lon_point='lat' WHERE id=$1`, dev); err != nil {
		t.Fatal(err)
	}
	s.pos.Reset()
	// after the swap, point 'lat' is the longitude source
	post(`{"device_id":"` + dev + `","readings":[{"point":"lat","value":13.7}]}`)
	if _, lon := pos(); lon == nil || *lon != 13.7 {
		t.Fatalf("after config swap lon = %v", lon)
	}

	// clearing the source stops position tracking
	if c := putSrc(`{"lat_point":null,"lon_point":null}`); c != 200 {
		t.Fatalf("clear got %d", c)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM devices WHERE id=$1 AND lat_point IS NULL AND lon_point IS NULL`, dev).Scan(&n)
	if n != 1 {
		t.Fatalf("source not cleared")
	}
	s.pos.Reset() // tracker still holds the swapped config
	post(`{"device_id":"` + dev + `","readings":[{"point":"lat","value":1}]}`)
	if lat, _ := pos(); lat == nil || *lat != 12.6 {
		t.Fatalf("cleared source still tracked: %v", lat)
	}

	// the config change was audited
	pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id='itest-ps1' AND action='device.location_source'`).Scan(&n)
	if n == 0 {
		t.Fatalf("no device.location_source audit rows")
	}
}
