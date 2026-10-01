package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLimiter(t *testing.T) {
	l := NewLimiter(60, 3) // 1/sec, burst 3
	now := time.Unix(1000, 0)
	l.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatalf("burst %d refused", i)
		}
	}
	ok, wait := l.Allow("k")
	if ok || wait <= 0 || wait > time.Second {
		t.Fatalf("expected refusal with <=1s wait, got %v %v", ok, wait)
	}
	if ok, _ := l.Allow("other"); !ok {
		t.Fatal("keys must be independent")
	}
	now = now.Add(1100 * time.Millisecond)
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("token should have refilled")
	}
	now = now.Add(time.Hour) // refill is capped at burst
	n := 0
	for ok, _ := l.Allow("k"); ok; ok, _ = l.Allow("k") {
		n++
	}
	if n != 3 {
		t.Fatalf("burst cap: got %d want 3", n)
	}
}

func TestMiddlewareRateLimitsKeysOnly(t *testing.T) {
	SetKeyLimiter(NewLimiter(60, 2))
	defer SetKeyLimiter(nil)
	resolver := func(_ context.Context, _ string) (string, string, string, []string, bool) {
		return "t", "apikey:1", "viewer", nil, true
	}
	h := Middleware([]byte("s"), resolver)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	codes := []int{}
	for i := 0; i < 3; i++ {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Authorization", "Bearer hxk_a.b")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		codes = append(codes, rec.Code)
		if rec.Code == 429 && rec.Header().Get("Retry-After") == "" {
			t.Fatal("429 without Retry-After")
		}
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 429 {
		t.Fatalf("codes %v", codes)
	}
}

func TestScopeAllows(t *testing.T) {
	cases := []struct {
		scopes []string
		path   string
		want   bool
	}{
		{nil, "/v1/anything", true},
		{[]string{"devices"}, "/v1/devices", true},
		{[]string{"devices"}, "/v1/devices/abc/telemetry", true},
		{[]string{"devices"}, "/v1/alerts", false},
		{[]string{"devices"}, "/v1/devicesx", false},
		{[]string{"devices"}, "/v1/", false},
		{[]string{"devices", "alerts"}, "/v1/alerts/1/ack", true},
	}
	for _, c := range cases {
		if got := ScopeAllows(c.scopes, c.path); got != c.want {
			t.Errorf("ScopeAllows(%v,%q)=%v want %v", c.scopes, c.path, got, c.want)
		}
	}
}
