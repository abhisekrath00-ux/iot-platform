package main

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The cache is tenant-scoped, serves repeat reads from memory, and any
// successful write drops the tenant's entries (stale config must not linger).
func TestIntegrationCacheHitIsolationAndInvalidation(t *testing.T) {
	s, _ := testServer(t)
	s.cache = newCache()
	seed(t, s, "itest-c1")
	seed(t, s, "itest-c2")
	api := http.NewServeMux()
	s.cached(api, "GET /v1/points", s.listPoints)
	api.HandleFunc("POST /v1/profiles", s.createProfile)
	h := s.invalidateOnWrite(api)

	w := call(h, "itest-c1", "viewer", "GET", "/v1/points", "")
	if w.Code != 200 || w.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("first read: %d %q", w.Code, w.Header().Get("X-Cache"))
	}
	w = call(h, "itest-c1", "viewer", "GET", "/v1/points", "")
	if w.Header().Get("X-Cache") != "HIT" {
		t.Fatal("second read must be a cache hit")
	}
	// another tenant must get its own data, not tenant c1's cached body
	w = call(h, "itest-c2", "viewer", "GET", "/v1/points", "")
	if w.Header().Get("X-Cache") != "MISS" || strings.Contains(w.Body.String(), "itest-c1-dev") {
		t.Fatalf("cross-tenant cache leak: %s", w.Body.String())
	}
	// data changes behind the cache are not visible until TTL/invalidation...
	if _, err := s.st.Pool.Exec(t.Context(), `INSERT INTO points(id,device_id,unit) VALUES('pressure','itest-c1-dev','bar') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	w = call(h, "itest-c1", "viewer", "GET", "/v1/points", "")
	if strings.Contains(w.Body.String(), "pressure") {
		t.Fatal("expected cached (stale) body before invalidation")
	}
	// ...and a successful write through the API drops them.
	if w := call(h, "itest-c1", "admin", "POST", "/v1/profiles", `{"name":"Cache probe `+strconv.FormatInt(time.Now().UnixNano(), 10)+`","driver_profile":"opcua","points":[{"id":"x","node_id":"ns=2;s=A.B","unit":"C","min":0,"max":400}]}`); w.Code >= 400 {
		t.Fatalf("profile create failed: %d %s", w.Code, w.Body.String())
	}
	w = call(h, "itest-c1", "viewer", "GET", "/v1/points", "")
	if !strings.Contains(w.Body.String(), "pressure") {
		t.Fatalf("write must invalidate; body: %s", w.Body.String())
	}
}
