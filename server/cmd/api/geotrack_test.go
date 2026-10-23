package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
)

func TestIntegrationDeviceTrack(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-trail")
	ctx := t.Context()
	pool := s.st.Pool
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM device_positions WHERE tenant_id='itest-trail'`) })
	pool.Exec(ctx, `DELETE FROM device_positions WHERE tenant_id='itest-trail'`)
	api := http.NewServeMux()
	api.HandleFunc("PUT /v1/devices/{id}/location", s.setDeviceLocation)
	api.HandleFunc("GET /v1/devices/{id}/track", s.getDeviceTrack)
	type point struct {
		Lat, Lon   float64
		At, Source string
	}
	type result struct {
		Points []point
		Total  int
	}
	read := func(hours string) result {
		t.Helper()
		w := call(api, "itest-trail", "viewer", "GET", "/v1/devices/itest-trail-dev/track?hours="+hours, "")
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		var r result
		if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	save := func(body string) {
		t.Helper()
		w := call(api, "itest-trail", "operator", "PUT", "/v1/devices/itest-trail-dev/location", body)
		if w.Code != 200 {
			t.Fatalf("save %d %s", w.Code, w.Body.String())
		}
	}
	save(`{"lat":12.97,"lon":77.59}`)
	save(`{"lat":12.97001,"lon":77.59}`)
	if r := read("24"); r.Total != 1 || r.Points[0].Source != "manual" {
		t.Fatalf("manual dedupe: %+v", r)
	}
	save(`{"lat":12.975,"lon":77.595}`)
	save(`{"lat":null,"lon":null}`)
	if r := read("24"); r.Total != 2 {
		t.Fatalf("move/clear: %+v", r)
	}
	// Telemetry uses the later axis timestamp, not processing time. Replays
	// never insert a new point, and stale unpaired axes do not publish a fix.
	pool.Exec(ctx, `UPDATE devices SET lat_point='latp',lon_point='lonp',lat_fix=NULL,lon_fix=NULL,lat_fix_at=NULL,lon_fix_at=NULL WHERE id='itest-trail-dev'`)
	tr := store.NewPositionTracker(pool)
	at := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Microsecond)
	for _, p := range []struct {
		id string
		v  float64
		at time.Time
	}{{"latp", 12.99, at}, {"lonp", 77.66, at.Add(time.Second)}, {"lonp", 77.66, at.Add(time.Second)}} {
		if err := tr.Track(ctx, "itest-trail", "itest-trail-dev", p.id, p.v, p.at); err != nil {
			t.Fatal(err)
		}
	}
	r := read("24")
	if r.Total != 3 || r.Points[0].Source != "telemetry" || r.Points[0].Lat != 12.99 {
		t.Fatalf("telemetry/order: %+v", r)
	}
	got, err := time.Parse(time.RFC3339Nano, r.Points[0].At)
	if err != nil || !got.Equal(at.Add(time.Second)) {
		t.Fatalf("fix time %v %v", got, err)
	}
	if r := read("1"); r.Total != 2 {
		t.Fatalf("window: %+v", r)
	}
	// Heartbeat and cleanup also happen when stationary. Pruning uses wall
	// clock, not a possibly old fix time. Inactive rows await next write.
	old := time.Now().Add(-8 * 24 * time.Hour)
	pool.Exec(ctx, `INSERT INTO device_positions VALUES('itest-trail','itest-trail-dev',1,2,$1,'manual')`, old)
	hb := time.Now().Add(-10 * time.Minute)
	if err := store.RecordTrail(ctx, pool, "itest-trail", "itest-trail-dev", 12.99, 77.66, hb, "manual"); err != nil {
		t.Fatal(err)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM device_positions WHERE device_id='itest-trail-dev' AND recorded_at<now()-interval '7 days'`).Scan(&n)
	if n != 0 {
		t.Fatal("stale rows not pruned")
	}
	for _, test := range []struct {
		tenant, path string
		code         int
	}{{"itest-trail", "/v1/devices/itest-trail-dev/track?hours=0", 400}, {"itest-trail", "/v1/devices/itest-trail-dev/track?hours=169", 400}, {"other", "/v1/devices/itest-trail-dev/track", 404}, {"itest-trail", "/v1/devices/nope/track", 404}} {
		w := call(api, test.tenant, "viewer", "GET", test.path, "")
		if w.Code != test.code {
			t.Fatalf("%s: %d", test.path, w.Code)
		}
	}
	if scopedAllows("GET", "/v1/devices/itest-trail-dev/track") {
		t.Fatal("tracks unexpectedly allowed to scoped users")
	}
	// Large trail: all selected locations and times must be real rows. Keep
	// first/last, return a truthful total, and bound the response to 720.
	pool.Exec(ctx, `DELETE FROM device_positions WHERE device_id='itest-trail-dev'`)
	_, err = pool.Exec(ctx, `INSERT INTO device_positions SELECT 'itest-trail','itest-trail-dev',20+g*0.0001,30,now()-interval '2 hours'+g*interval '1 second','telemetry' FROM generate_series(0,799) g`)
	if err != nil {
		t.Fatal(err)
	}
	r = read("24")
	if r.Total != 800 || len(r.Points) > 720 || len(r.Points) < 2 || r.Points[0].Lat != 20 || r.Points[len(r.Points)-1].Lat != 20.0799 {
		t.Fatalf("sample %+v", r)
	}
	for i, p := range r.Points {
		if i > 0 && p.At <= r.Points[i-1].At {
			t.Fatal("unordered")
		}
		pool.QueryRow(ctx, `SELECT count(*) FROM device_positions WHERE device_id='itest-trail-dev' AND lat=$1 AND recorded_at=$2`, p.Lat, p.At).Scan(&n)
		if n != 1 {
			t.Fatal("not a real fix")
		}
	}
	// A one-point/empty track must not cause SQL modulo-by-zero.
	pool.Exec(ctx, `DELETE FROM device_positions WHERE device_id='itest-trail-dev'`)
	if read("24").Total != 0 {
		t.Fatal("empty")
	}
	save(`{"lat":1,"lon":2}`)
	if read("24").Total != 1 {
		t.Fatal("single")
	}
}
