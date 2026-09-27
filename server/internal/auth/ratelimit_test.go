package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterBurstThenReject(t *testing.T) {
	rl := NewRateLimiter(60, 3) // 1/sec refill, burst 3
	defer rl.Close()
	now := time.Now()
	rl.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if !rl.allow("1.2.3.4") {
			t.Fatalf("burst %d rejected", i)
		}
	}
	if rl.allow("1.2.3.4") {
		t.Fatal("4th request should be rejected")
	}
	if !rl.allow("5.6.7.8") {
		t.Fatal("other IP must be independent")
	}
	now = now.Add(2 * time.Second) // refill 2 tokens
	if !rl.allow("1.2.3.4") {
		t.Fatal("refill failed")
	}
}

func TestRateLimiterHTTP429(t *testing.T) {
	rl := NewRateLimiter(0, 1)
	defer rl.Close()
	ok := false
	h := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ok = true }))
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "9.9.9.9:1234"
	h.ServeHTTP(httptest.NewRecorder(), r)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !ok || w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("ok=%v code=%d", ok, w.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	for k, v := range map[string]string{
		"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY",
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
	} {
		if w.Header().Get(k) != v {
			t.Fatalf("%s = %q", k, w.Header().Get(k))
		}
	}
}
