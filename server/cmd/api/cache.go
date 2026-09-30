package main

import (
	"net/http"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/respcache"
)

// Cache TTLs per hot read endpoint. Live values stay within a couple of
// seconds; configuration-shaped lists can be older because every successful
// mutation invalidates the tenant's entries (see invalidateOnWrite).
var cacheTTL = map[string]time.Duration{
	"GET /v1/fleet":            2 * time.Second,
	"GET /v1/telemetry/latest": 2 * time.Second,
	"GET /v1/devices":          10 * time.Second,
	"GET /v1/points":           15 * time.Second,
	"GET /v1/sites":            30 * time.Second,
	"GET /v1/profiles":         30 * time.Second,
}

// cached registers a GET route behind the response cache when it has a TTL.
func (s *server) cached(mux *http.ServeMux, pattern string, h http.HandlerFunc) {
	if ttl, ok := cacheTTL[pattern]; ok && s.cache != nil {
		h = s.cache.Middleware(ttl, auth.Tenant, h)
	}
	mux.HandleFunc(pattern, h)
}

// invalidateOnWrite drops a tenant's cached reads after any successful
// mutating request. Over-invalidation only costs a recompute.
func (s *server) invalidateOnWrite(next http.Handler) http.Handler {
	if s.cache == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(sw, r)
		if sw.code < 400 {
			s.cache.Invalidate(auth.Tenant(r))
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(c int) { w.code = c; w.ResponseWriter.WriteHeader(c) }

func newCache() *respcache.Cache { return respcache.New(2000) }
