package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestParseImportCSV(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	rows, p := parseImportCSV(strings.NewReader("ts,point,value\n2026-10-01T00:00:00Z,temp,21.5\n"), now)
	if len(p) != 0 || len(rows) != 1 || rows[0].value != 21.5 {
		t.Fatalf("good file: %v %v", rows, p)
	}
	_, p = parseImportCSV(strings.NewReader("ts,point,value\nyesterday,temp,1\n2026-10-01T00:00:00Z,,1\n2026-10-01T00:00:00Z,temp,NaN\n2030-01-01T00:00:00Z,temp,1\n2000-01-01T00:00:00Z,temp,1\n"), now)
	if len(p) != 5 {
		t.Fatalf("want 5 problems, got %v", p)
	}
	if _, p = parseImportCSV(strings.NewReader("ts,value\n"), now); len(p) == 0 {
		t.Fatal("missing column accepted")
	}
}

func TestIntegrationTelemetryImport(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "timp")
	seed(t, s, "timp2")
	ctx := context.Background()
	clean := func() {
		for _, q := range []string{`DELETE FROM telemetry WHERE tenant_id IN ('timp','timp2')`, `DELETE FROM telemetry_rollup_hourly WHERE tenant_id IN ('timp','timp2')`, `DELETE FROM audit_log WHERE tenant_id IN ('timp','timp2')`} {
			s.st.Pool.Exec(ctx, q)
		}
	}
	clean()
	t.Cleanup(clean)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/telemetry/import", s.importTelemetry)
	day := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Hour)
	f := func(d time.Duration) string { return day.Add(d).Format(time.RFC3339) }
	good := "ts,point,value\n" + f(0) + ",temp,10\n" + f(10*time.Minute) + ",temp,20\n" + f(time.Hour) + ",temp,30\n"
	base := "/v1/telemetry/import?device_id=timp-dev"
	if c := call(mux, "timp", "operator", "POST", base, good).Code; c != 403 {
		t.Fatalf("operator import = %d, want 403", c)
	}
	if c := call(mux, "timp2", "admin", "POST", base, good).Code; c != 404 {
		t.Fatalf("cross-tenant import = %d, want 404", c)
	}
	if w := call(mux, "timp", "admin", "POST", base, good+f(0)+",nope,1\n"); w.Code != 422 || !strings.Contains(w.Body.String(), "unknown point") {
		t.Fatalf("bad file: %d %s", w.Code, w.Body)
	}
	var n int
	s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM telemetry WHERE tenant_id='timp'`).Scan(&n)
	if n != 0 {
		t.Fatalf("rejected file stored %d rows", n)
	}
	if w := call(mux, "timp", "admin", "POST", base+"&dry_run=1", good); w.Code != 200 {
		t.Fatalf("dry run: %d %s", w.Code, w.Body)
	}
	s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM telemetry WHERE tenant_id='timp'`).Scan(&n)
	if n != 0 {
		t.Fatal("dry run stored rows")
	}
	w := call(mux, "timp", "admin", "POST", base, good)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"imported":3`) {
		t.Fatalf("import: %d %s", w.Code, w.Body)
	}
	var cnt int64
	var sum float64
	if err := s.st.Pool.QueryRow(ctx, `SELECT n, sum FROM telemetry_rollup_hourly WHERE tenant_id='timp' AND device_id='timp-dev' AND point_id='temp' AND bucket=$1`, day).Scan(&cnt, &sum); err != nil || cnt != 2 || sum != 30 {
		t.Fatalf("rollup for first hour: n=%d sum=%v err=%v", cnt, sum, err)
	}
	if w := call(mux, "timp", "admin", "POST", base, good); !strings.Contains(w.Body.String(), `"imported":0`) || !strings.Contains(w.Body.String(), `"duplicates":3`) {
		t.Fatalf("re-upload not idempotent: %s", w.Body)
	}
	// A rollup that summarises more samples than raw data holds (raw purged) must survive.
	s.st.Pool.Exec(ctx, `UPDATE telemetry_rollup_hourly SET n=50, sum=500 WHERE tenant_id='timp' AND bucket=$1`, day)
	call(mux, "timp", "admin", "POST", base, "ts,point,value\n"+f(20*time.Minute)+",temp,5\n")
	s.st.Pool.QueryRow(ctx, `SELECT n FROM telemetry_rollup_hourly WHERE tenant_id='timp' AND bucket=$1`, day).Scan(&cnt)
	if cnt != 50 {
		t.Fatalf("purged-data rollup was overwritten: n=%d", cnt)
	}
}
