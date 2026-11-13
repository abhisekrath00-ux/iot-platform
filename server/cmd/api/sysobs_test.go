package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestObsRedact(t *testing.T) {
	in := "login failed password=hunter2hunter2 token: abcdef123456 postgres://iot:s3cr3tpw@db:5432/iot Authorization: Bearer abc.def.ghi key sk-abcdefghijklmnop1234"
	out := obsRedact(in)
	for _, leak := range []string{"hunter2hunter2", "abcdef123456", "s3cr3tpw", "abc.def.ghi", "abcdefghijklmnop1234"} {
		if strings.Contains(out, leak) {
			t.Errorf("%q leaked in %q", leak, out)
		}
	}
	if !strings.Contains(out, "login failed") {
		t.Errorf("message lost: %q", out)
	}
}

func TestObsTeeKeepsOnlyWarnAndError(t *testing.T) {
	for len(obs.logCh) > 0 {
		<-obs.logCh
	}
	obsTee{}.Write([]byte("2026/10/05 api listening on :8000\n"))
	obsTee{}.Write([]byte("2026/10/05 db connection refused, retrying\n"))
	obsTee{}.Write([]byte("2026/10/05 request failed: boom\n"))
	if n := len(obs.logCh); n != 2 {
		t.Fatalf("kept %d lines, want 2 (warn + error)", n)
	}
}

func TestIntegrationSystemObservability(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-obs")
	ctx := t.Context()
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/system/health", s.systemHealth)
	api.HandleFunc("GET /v1/system/metrics", s.systemMetrics)
	api.HandleFunc("GET /v1/system/logs", s.systemLogs)
	api.HandleFunc("GET /v1/system/prom", s.systemProm)
	api.HandleFunc("GET /v1/system/support", s.systemSupport)
	s.st.Pool.Exec(ctx, `DELETE FROM obs_logs WHERE message LIKE 'itest-obs%'`)
	t.Cleanup(func() { s.st.Pool.Exec(ctx, `DELETE FROM obs_logs WHERE message LIKE 'itest-obs%'`) })

	for _, p := range []string{"/v1/system/health", "/v1/system/metrics?names=api_p95_ms", "/v1/system/logs", "/v1/system/prom", "/v1/system/support"} {
		if w := call(api, "itest-obs", "operator", "GET", p, ""); w.Code != 403 {
			t.Fatalf("%s as operator: %d", p, w.Code)
		}
	}
	obsTee{}.Write([]byte("itest-obs error: login failed password=hunter2hunter2\n"))
	rctx, cancel := context.WithTimeout(ctx, 3500*time.Millisecond)
	defer cancel()
	s.obsRun(rctx) // takes one sample at once and drains the log channel on its 2 s tick

	w := call(api, "itest-obs", "admin", "GET", "/v1/system/health", "")
	var h map[string]any
	json.Unmarshal(w.Body.Bytes(), &h)
	cur, _ := h["current"].(map[string]any)
	if w.Code != 200 || cur["db_size_mb"] == nil || cur["api_goroutines"] == nil || h["sources"] == nil {
		t.Fatalf("health: %d %s", w.Code, w.Body.String())
	}
	w = call(api, "itest-obs", "admin", "GET", "/v1/system/metrics?names=db_size_mb,api_goroutines&minutes=5", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "db_size_mb") {
		t.Fatalf("metrics: %d %s", w.Code, w.Body.String())
	}
	if w := call(api, "itest-obs", "admin", "GET", "/v1/system/metrics", ""); w.Code != 400 {
		t.Fatalf("metrics without names: %d", w.Code)
	}
	w = call(api, "itest-obs", "admin", "GET", "/v1/system/logs?level=error&q=itest-obs", "")
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("logs cacheable")
	}
	if w.Code != 200 || !strings.Contains(w.Body.String(), "login failed") || strings.Contains(w.Body.String(), "hunter2hunter2") {
		t.Fatalf("logs (must be stored redacted): %d %s", w.Code, w.Body.String())
	}
	if w := call(api, "itest-obs", "admin", "GET", "/v1/system/prom", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "hexthings_db_size_mb") {
		t.Fatalf("prom: %d %s", w.Code, w.Body.String())
	}
	w = call(api, "itest-obs", "admin", "GET", "/v1/system/support", "")
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Disposition") == "" {
		t.Fatal("bundle headers")
	}
	if w.Code != 200 || strings.Contains(w.Body.String(), "hunter2hunter2") || !strings.Contains(w.Body.String(), "[current numbers]") {
		t.Fatalf("support: %d", w.Code)
	}
}

func TestIntegrationCacheAndPoolObservability(t *testing.T) {
	s, _ := testServer(t)
	s.cache = newCache()
	s.cache.Hits.Add(4)
	s.cache.Misses.Add(3)
	s.cache.Invalidations.Add(2)
	s.cache.SharedReadFailures.Add(5)
	s.cache.SharedWriteFailures.Add(6)
	s.cache.SharedInvalidationFailures.Add(7)
	m := s.obsSnapshot(t.Context())
	expected := map[string]float64{"cache_enabled": 1, "cache_hits_total": 4, "cache_misses_total": 3, "cache_invalidations_total": 2, "cache_shared_read_failures_total": 5, "cache_shared_write_failures_total": 6, "cache_shared_invalidation_failures_total": 7}
	for k, v := range expected {
		if m[k] != v {
			t.Errorf("%s=%g want %g", k, m[k], v)
		}
	}
	for _, k := range []string{"db_pool_acquired", "db_pool_idle", "db_pool_total", "db_pool_max", "db_pool_acquires_total", "db_pool_empty_acquires_total", "db_pool_canceled_acquires_total", "db_pool_acquire_seconds_total"} {
		if _, ok := m[k]; !ok {
			t.Error("missing pool signal", k)
		}
	}
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/system/prom", s.systemProm)
	w := call(api, "itest-obs", "admin", "GET", "/v1/system/prom", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "# TYPE hexthings_cache_shared_invalidation_failures_total counter\nhexthings_cache_shared_invalidation_failures_total 7") {
		t.Fatalf("failure counter not correctly exported: %s", w.Body.String())
	}
	if w := call(api, "itest-obs", "operator", "GET", "/v1/system/prom", ""); w.Code != 403 {
		t.Fatal("cache/pool metrics exposed to non-admin")
	}
	// Wait for the actual persisted condition, not a 100ms scheduling guess.
	// Slower CI storage must not be confused with a missing export.
	sampleCtx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	done := make(chan struct{})
	go func() { defer close(done); s.obsRun(sampleCtx) }()
	defer func() { cancel(); <-done }()
	started := time.Now()
	for {
		var count int
		err := s.st.Pool.QueryRow(sampleCtx, `SELECT count(DISTINCT name) FROM obs_metrics WHERE ts >= $1 AND name IN ('cache_hits_total','cache_shared_invalidation_failures_total','db_pool_max','db_pool_acquire_seconds_total')`, started.Add(-time.Second)).Scan(&count)
		if err == nil && count == 4 {
			break
		}
		if sampleCtx.Err() != nil {
			t.Fatalf("cache/pool sampler omitted signals: count=%d err=%v", count, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	cancel()
	<-done
	// The deferred receive remains safe once the channel is closed.
	s.cache = nil
	m = s.obsSnapshot(t.Context())
	if m["cache_enabled"] != 0 {
		t.Fatal("disabled cache shown as enabled")
	}
	if _, ok := m["cache_hits_total"]; ok {
		t.Fatal("disabled cache has misleading hit count")
	}
}
