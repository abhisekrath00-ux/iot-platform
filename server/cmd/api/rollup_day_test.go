package main

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"testing"
)

func TestIntegrationRollupDailyMergesHourly(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-ru1")
	ctx := t.Context()
	pool := s.st.Pool
	clean := func() {
		pool.Exec(context.Background(), `DELETE FROM telemetry_rollup_hourly WHERE tenant_id='itest-ru1'`)
	}
	clean()
	t.Cleanup(clean)
	// Day 1: two hours (n=2 sum=10 min=1 max=9) and (n=3 sum=30 min=5 max=15). Day 2: one hour.
	for _, q := range []string{
		`INSERT INTO telemetry_rollup_hourly VALUES('itest-ru1','d','p','2026-03-01T01:00:00Z',2,10,1,9)`,
		`INSERT INTO telemetry_rollup_hourly VALUES('itest-ru1','d','p','2026-03-01T23:00:00Z',3,30,5,15)`,
		`INSERT INTO telemetry_rollup_hourly VALUES('itest-ru1','d','p','2026-03-02T00:00:00Z',1,7,7,7)`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/telemetry/rollup", s.rollupTelemetry)
	get := func(extra string) []map[string]any {
		w := call(api, "itest-ru1", "viewer", "GET", "/v1/telemetry/rollup?device_id=d&point_id=p&from=2026-02-28T00:00:00Z&to=2026-03-10T00:00:00Z"+extra, "")
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		var out []map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	if h := get(""); len(h) != 3 {
		t.Fatalf("hourly rows = %d, want 3", len(h))
	}
	d := get("&bucket=day")
	if len(d) != 2 {
		t.Fatalf("daily rows = %d, want 2", len(d))
	}
	f := func(m map[string]any, k string) float64 { return m[k].(float64) }
	if f(d[0], "n") != 5 || math.Abs(f(d[0], "avg")-8) > 1e-9 || f(d[0], "min") != 1 || f(d[0], "max") != 15 {
		t.Fatalf("day1 = %v (want n=5 avg=8 min=1 max=15)", d[0])
	}
	if f(d[1], "n") != 1 || f(d[1], "avg") != 7 {
		t.Fatalf("day2 = %v", d[1])
	}
	if w := call(api, "itest-ru1", "viewer", "GET", "/v1/telemetry/rollup?device_id=d&point_id=p&bucket=week", ""); w.Code != 400 {
		t.Fatalf("bad bucket = %d", w.Code)
	}
}
