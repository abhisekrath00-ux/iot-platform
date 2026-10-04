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

func TestClientIP(t *testing.T) {
	proxy := ParseTrustedProxies("172.18.0.0/16, 10.0.0.9, junk, ")
	if len(proxy) != 2 {
		t.Fatalf("parsed %d entries", len(proxy))
	}
	req := func(remote string, xff ...string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		for _, x := range xff {
			r.Header.Add("X-Forwarded-For", x)
		}
		return r
	}
	for _, c := range []struct {
		name string
		r    *http.Request
		want string
	}{
		{"no trusted proxy: header ignored", req("203.0.113.5:4000", "1.2.3.4"), "203.0.113.5"},
		{"direct client claims a header: ignored", req("203.0.113.5:4000", "198.51.100.1"), "203.0.113.5"},
		{"via the proxy: header used", req("172.18.0.3:5000", "198.51.100.7"), "198.51.100.7"},
		{"spoofed extra entry on the left is ignored", req("172.18.0.3:5000", "9.9.9.9, 198.51.100.7"), "198.51.100.7"},
		{"two proxies", req("172.18.0.3:5000", "198.51.100.7, 10.0.0.9"), "198.51.100.7"},
		{"proxy without header", req("172.18.0.3:5000"), "172.18.0.3"},
		{"malformed header: fall back", req("172.18.0.3:5000", "not-an-ip"), "172.18.0.3"},
	} {
		trusted := proxy
		if c.name == "no trusted proxy: header ignored" {
			trusted = nil
		}
		if got := clientIP(c.r, trusted); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}
