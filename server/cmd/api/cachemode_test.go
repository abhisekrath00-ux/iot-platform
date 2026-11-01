package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestResponseCacheModes(t *testing.T) {
	for _, mode := range []string{"", "local"} {
		c, e := responseCache(mode)
		if e != nil || c == nil {
			t.Fatalf("local %q %v", mode, e)
		}
	}
	if c, e := responseCache("off"); c != nil || e != nil {
		t.Fatal("off must disable cache", e)
	}
	for _, mode := range []string{"typo", "OFF"} {
		if _, e := responseCache(mode); e == nil {
			t.Fatalf("accepted %q", mode)
		}
	}
	if c, e := responseCache("redis"); c == nil || e != nil {
		t.Fatal(e)
	}
	t.Setenv("CACHE_BACKEND", "off")
	if newCache() != nil {
		t.Fatal("production constructor ignored off")
	}
	t.Setenv("CACHE_BACKEND", "local")
	if newCache() == nil {
		t.Fatal("production constructor disabled local")
	}

}
func TestIntegrationOffCacheReplicasReadDatabaseWrites(t *testing.T) {
	a, _ := testServer(t)
	seed(t, a, "itest-cacheoff")
	b := &server{st: a.st}
	handlers := []http.Handler{}
	for _, s := range []*server{a, b} {
		var err error
		s.cache, err = responseCache("off")
		if err != nil {
			t.Fatal(err)
		}
		mux := http.NewServeMux()
		s.cached(mux, "GET /v1/points", s.listPoints)
		handlers = append(handlers, s.invalidateOnWrite(mux))
	}
	for _, h := range handlers {
		for range 2 {
			w := call(h, "itest-cacheoff", "viewer", "GET", "/v1/points", "")
			if w.Code != 200 || w.Header().Get("X-Cache") != "" || strings.Contains(w.Body.String(), "pressure") {
				t.Fatal(w.Code, w.Body.String())
			}
		}
	}
	if _, err := a.st.Pool.Exec(t.Context(), `INSERT INTO points(id,device_id,unit) VALUES('pressure','itest-cacheoff-dev','bar')`); err != nil {
		t.Fatal(err)
	}
	for _, h := range handlers {
		w := call(h, "itest-cacheoff", "viewer", "GET", "/v1/points", "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "pressure") || w.Header().Get("X-Cache") != "" {
			t.Fatal("replica stale", w.Code, w.Body.String())
		}
	}
	a.st.Close()
	for _, h := range handlers {
		w := call(h, "itest-cacheoff", "viewer", "GET", "/v1/points", "")
		if w.Code < 500 || strings.Contains(w.Body.String(), "pressure") {
			t.Fatal("must not return cached success on DB outage", w.Code, w.Body.String())
		}
	}
}
