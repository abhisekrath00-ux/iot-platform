package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/retention"
)

func TestIntegrationRollupAndPurge(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-rt1")
	seed(t, s, "itest-rt2")
	pool := s.st.Pool
	ctx := t.Context()
	pool.Exec(ctx, `DELETE FROM telemetry_rollup_hourly WHERE tenant_id IN ('itest-rt1','itest-rt2')`)
	pool.Exec(ctx, `DELETE FROM telemetry WHERE tenant_id='itest-rt1'`)

	// Two hours of old data: 10 values 1..10 in hour A, one 'missing' row that must not count.
	base := time.Now().UTC().Add(-10 * 24 * time.Hour).Truncate(time.Hour)
	for i := 1; i <= 10; i++ {
		_, err := pool.Exec(ctx, `INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,value,unit,schema_version)
			VALUES($1,'itest-rt1','itest-rt1-gw','itest-rt1-dev','temp',$2,$3,'C',1)`, "rt-a"+string(rune('a'+i)), base.Add(time.Duration(i)*time.Minute), float64(i))
		if err != nil {
			t.Fatal(err)
		}
	}
	pool.Exec(ctx, `INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,value,unit,quality,schema_version)
		VALUES('rt-missing','itest-rt1','itest-rt1-gw','itest-rt1-dev','temp',$1,999,'C','missing',1)`, base.Add(30*time.Minute))
	// One recent row that retention must keep.
	pool.Exec(ctx, `INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,value,unit,schema_version)
		VALUES('rt-recent','itest-rt1','itest-rt1-gw','itest-rt1-dev','temp',now(),5,'C',1)`)

	if n, err := retention.Rollup(ctx, pool, base, base.Add(time.Hour)); err != nil || n != 1 {
		t.Fatalf("rollup n=%d err=%v", n, err)
	}
	// idempotent
	if _, err := retention.Rollup(ctx, pool, base, base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var cnt int64
	var sum, mn, mx float64
	if err := pool.QueryRow(ctx, `SELECT n,sum,min,max FROM telemetry_rollup_hourly WHERE tenant_id='itest-rt1' AND bucket=$1`, base).Scan(&cnt, &sum, &mn, &mx); err != nil {
		t.Fatal(err)
	}
	if cnt != 10 || sum != 55 || mn != 1 || mx != 10 {
		t.Fatalf("rollup = n%d sum%v min%v max%v", cnt, sum, mn, mx)
	}

	// Purge raw older than 7 days: old rows go (including the 'missing' row), rollup and recent stay.
	del, err := retention.Purge(ctx, pool, time.Now().Add(-7*24*time.Hour))
	if err != nil || del != 11 {
		t.Fatalf("purge deleted %d err=%v, want 11", del, err)
	}
	var raw int
	pool.QueryRow(ctx, `SELECT count(*) FROM telemetry WHERE tenant_id='itest-rt1'`).Scan(&raw)
	if raw != 1 {
		t.Fatalf("raw rows left = %d, want 1", raw)
	}

	api := http.NewServeMux()
	api.HandleFunc("GET /v1/telemetry/rollup", s.rollupTelemetry)
	path := "/v1/telemetry/rollup?device_id=itest-rt1-dev&point_id=temp&from=" + base.Add(-time.Hour).Format(time.RFC3339) + "&to=" + base.Add(2*time.Hour).Format(time.RFC3339)
	w := call(api, "itest-rt1", "viewer", "GET", path, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"avg":5.5`) || !strings.Contains(w.Body.String(), `"n":10`) {
		t.Fatalf("rollup api = %d %s", w.Code, w.Body.String())
	}
	if w := call(api, "itest-rt2", "viewer", "GET", path, ""); strings.Contains(w.Body.String(), `"avg"`) {
		t.Fatalf("tenant leak: %s", w.Body.String())
	}
	if w := call(api, "itest-rt1", "viewer", "GET", "/v1/telemetry/rollup?device_id=x", ""); w.Code != 400 {
		t.Fatalf("missing point = %d", w.Code)
	}
	if w := call(api, "itest-rt1", "viewer", "GET", "/v1/telemetry/rollup?device_id=a&point_id=b&from=2020-01-01T00:00:00Z&to=2026-01-01T00:00:00Z", ""); w.Code != 400 {
		t.Fatalf("huge range = %d", w.Code)
	}
}
