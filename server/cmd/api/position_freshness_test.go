package main

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
)

// Freshness and provenance for live positions: stale, duplicated or
// out-of-order readings never move a device backwards, and a position is
// published only from a latitude/longitude pair that agrees in time.
func TestIntegrationLocationSourceFreshness(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-pf1")
	s.pos = store.NewPositionTracker(s.st.Pool)
	ctx := context.Background()
	pool := s.st.Pool
	dev := "itest-pf1-dev"
	if _, err := pool.Exec(ctx, `INSERT INTO points(id,device_id,unit) VALUES('lat','itest-pf1-dev','deg'),('lon','itest-pf1-dev','deg')`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM telemetry WHERE tenant_id='itest-pf1'`)
		pool.Exec(ctx, `DELETE FROM devices WHERE tenant_id='itest-pf1'`)
	})
	api := http.NewServeMux()
	api.HandleFunc("PUT /v1/devices/{id}/location-source", s.setDeviceLocationSource)
	api.HandleFunc("PUT /v1/devices/{id}/location", s.setDeviceLocation)
	api.HandleFunc("POST /v1/telemetry/ingest", s.ingestHTTP)
	if c := call(api, "itest-pf1", "operator", "PUT", "/v1/devices/"+dev+"/location-source", `{"lat_point":"lat","lon_point":"lon"}`).Code; c != 200 {
		t.Fatalf("config got %d", c)
	}
	post := func(body string) int {
		return call(api, "itest-pf1", "operator", "POST", "/v1/telemetry/ingest", body).Code
	}
	pos := func() (lat, lon *float64) {
		if err := pool.QueryRow(ctx, `SELECT lat, lon FROM devices WHERE id=$1`, dev).Scan(&lat, &lon); err != nil {
			t.Fatal(err)
		}
		return
	}
	ts := func(d time.Duration) string { return time.Now().Add(d).UTC().Format(time.RFC3339) }

	// A fix pairs and publishes.
	post(fmt.Sprintf(`{"device_id":%q,"readings":[{"point":"lat","value":10,"ts":%q},{"point":"lon","value":20,"ts":%q}]}`, dev, ts(-30*time.Second), ts(-20*time.Second)))
	if lat, lon := pos(); lat == nil || *lat != 10 || lon == nil || *lon != 20 {
		t.Fatalf("initial pair = %v,%v", lat, lon)
	}
	// An explicitly older latitude never moves the axis backwards.
	post(fmt.Sprintf(`{"device_id":%q,"readings":[{"point":"lat","value":99,"ts":%q}]}`, dev, ts(-time.Minute)))
	if lat, _ := pos(); lat == nil || *lat != 10 {
		t.Fatalf("stale lat moved position: %v", lat)
	}
	// A replayed duplicate (same event_id and ts) is not re-tracked.
	post(fmt.Sprintf(`{"device_id":%q,"readings":[{"point":"lat","value":10,"event_id":"pf-dup","ts":%q}]}`, dev, ts(-15*time.Second)))
	post(fmt.Sprintf(`{"device_id":%q,"readings":[{"point":"lat","value":10,"event_id":"pf-dup","ts":%q}]}`, dev, ts(-15*time.Second)))
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM telemetry WHERE tenant_id='itest-pf1' AND event_id='pf-dup'`).Scan(&n)
	if n != 1 {
		t.Fatalf("duplicate stored %d times", n)
	}
	if lat, _ := pos(); lat == nil || *lat != 10 {
		t.Fatalf("replay moved position: %v", lat)
	}
	// A hand-set position stands until the next paired telemetry fix.
	if c := call(api, "itest-pf1", "operator", "PUT", "/v1/devices/"+dev+"/location", `{"lat":1.5,"lon":2.5}`).Code; c != 200 {
		t.Fatalf("manual set got %d", c)
	}
	if lat, lon := pos(); lat == nil || *lat != 1.5 || lon == nil || *lon != 2.5 {
		t.Fatalf("manual = %v,%v", lat, lon)
	}
	post(fmt.Sprintf(`{"device_id":%q,"readings":[{"point":"lat","value":11,"ts":%q},{"point":"lon","value":21,"ts":%q}]}`, dev, ts(-10*time.Second), ts(-5*time.Second)))
	if lat, lon := pos(); lat == nil || *lat != 11 || lon == nil || *lon != 21 {
		t.Fatalf("telemetry did not take over: %v,%v", lat, lon)
	}

	// Pairing window: axes too far apart in time publish nothing.
	dev2 := "itest-pf1-dev2"
	for _, q := range []string{
		`INSERT INTO devices(id,tenant_id,gateway_id,profile,name) VALUES('itest-pf1-dev2','itest-pf1','itest-pf1-gw','modbus-tcp','Truck two')`,
		`INSERT INTO points(id,device_id,unit) VALUES('lat','itest-pf1-dev2','deg'),('lon','itest-pf1-dev2','deg')`,
		`UPDATE devices SET lat_point='lat', lon_point='lon' WHERE id='itest-pf1-dev2'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	s.pos.Reset()
	pos2 := func() (lat, lon *float64) {
		pool.QueryRow(ctx, `SELECT lat, lon FROM devices WHERE id=$1`, dev2).Scan(&lat, &lon)
		return
	}
	// lat 10 minutes ago, lon now: 10 minutes apart, over the window.
	post(fmt.Sprintf(`{"device_id":%q,"readings":[{"point":"lat","value":30,"ts":%q}]}`, dev2, ts(-10*time.Minute)))
	post(fmt.Sprintf(`{"device_id":%q,"readings":[{"point":"lon","value":40,"ts":%q}]}`, dev2, ts(-5*time.Second)))
	if lat, lon := pos2(); lat != nil || lon != nil {
		t.Fatalf("out-of-window pair published %v,%v", lat, lon)
	}
	// A fresh latitude close to the stored longitude completes the pair.
	post(fmt.Sprintf(`{"device_id":%q,"readings":[{"point":"lat","value":31,"ts":%q}]}`, dev2, ts(-2*time.Second)))
	if lat, lon := pos2(); lat == nil || *lat != 31 || lon == nil || *lon != 40 {
		t.Fatalf("in-window pair = %v,%v", lat, lon)
	}

	// A config change through the endpoint takes effect on this process at
	// once (no cache reset in the test): re-point dev2 at new points.
	for _, q := range []string{
		`INSERT INTO points(id,device_id,unit) VALUES('la2','itest-pf1-dev2','deg'),('lo2','itest-pf1-dev2','deg')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if c := call(api, "itest-pf1", "operator", "PUT", "/v1/devices/"+dev2+"/location-source", `{"lat_point":"la2","lon_point":"lo2"}`).Code; c != 200 {
		t.Fatalf("reconfig got %d", c)
	}
	post(fmt.Sprintf(`{"device_id":%q,"readings":[{"point":"la2","value":50,"ts":%q},{"point":"lo2","value":60,"ts":%q}]}`, dev2, ts(0), ts(0)))
	if lat, lon := pos2(); lat == nil || *lat != 50 || lon == nil || *lon != 60 {
		t.Fatalf("after endpoint reconfig = %v,%v", lat, lon)
	}
}
