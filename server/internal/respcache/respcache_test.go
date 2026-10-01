package respcache

import (
	"github.com/abhisekrath00-ux/iot-platform/server/internal/redisx"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/redisx/redisxtest"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func tenantHdr(r *http.Request) string { return r.Header.Get("T") }

func get(h http.HandlerFunc, tenant, uri string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", uri, nil)
	if tenant != "" {
		r.Header.Set("T", tenant)
	}
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestHitMissTenantIsolationInvalidateAndTTL(t *testing.T) {
	c := New(100)
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	var n atomic.Int32
	h := c.Middleware(5*time.Second, tenantHdr, func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"t":"` + r.Header.Get("T") + `"}`))
	})
	if w := get(h, "a", "/x?q=1"); w.Header().Get("X-Cache") != "MISS" {
		t.Fatal("first call must miss")
	}
	if w := get(h, "a", "/x?q=1"); w.Header().Get("X-Cache") != "HIT" || n.Load() != 1 {
		t.Fatal("second call must hit")
	}
	if w := get(h, "b", "/x?q=1"); w.Body.String() != `{"t":"b"}` || n.Load() != 2 {
		t.Fatalf("tenant b must not see tenant a's entry: %s", w.Body.String())
	}
	get(h, "a", "/x?q=2")
	if n.Load() != 3 {
		t.Fatal("different query must be a different key")
	}
	c.Invalidate("a")
	get(h, "a", "/x?q=1")
	if n.Load() != 4 {
		t.Fatal("invalidate must force a fresh call")
	}
	get(h, "b", "/x?q=1")
	if n.Load() != 4 {
		t.Fatal("invalidating a must not touch b")
	}
	now = now.Add(6 * time.Second)
	get(h, "b", "/x?q=1")
	if n.Load() != 5 {
		t.Fatal("expired entry must refetch")
	}
	get(h, "", "/x")
	get(h, "", "/x")
	if n.Load() != 7 {
		t.Fatal("no tenant must bypass the cache")
	}
}

func TestErrorsAreNotCachedAndPostBypasses(t *testing.T) {
	c := New(10)
	var n atomic.Int32
	h := c.Middleware(time.Minute, tenantHdr, func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		http.Error(w, "boom", 500)
	})
	get(h, "a", "/e")
	w := get(h, "a", "/e")
	if w.Code != 500 || n.Load() != 2 {
		t.Fatalf("5xx must not be cached: code=%d calls=%d", w.Code, n.Load())
	}
	r := httptest.NewRequest("POST", "/e", nil)
	r.Header.Set("T", "a")
	h(httptest.NewRecorder(), r)
	if n.Load() != 3 {
		t.Fatal("non-GET must bypass")
	}
}

func TestSingleFlightUnderConcurrency(t *testing.T) {
	c := New(10)
	var n atomic.Int32
	release := make(chan struct{})
	h := c.Middleware(time.Minute, tenantHdr, func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		<-release
		w.Write([]byte("ok"))
	})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); get(h, "a", "/s") }()
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()
	if n.Load() != 1 {
		t.Fatalf("backend called %d times for 20 concurrent misses, want 1", n.Load())
	}
}

func TestBounded(t *testing.T) {
	c := New(5)
	h := c.Middleware(time.Minute, tenantHdr, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("x")) })
	for i := 0; i < 50; i++ {
		get(h, "a", "/k"+uitoa(uint64(i)))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) > 5 {
		t.Fatalf("cache grew to %d entries, max 5", len(c.m))
	}
}

func TestSharedCacheAcrossReplicasAndOutage(t *testing.T) {
	srv := redisxtest.Start()
	defer srv.Close()
	cli := redisx.New(srv.Addr, "pw")
	var n atomic.Int32
	backend := func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"t":"` + r.Header.Get("T") + `","n":` + string(rune('0'+n.Load())) + `}`))
	}
	a, b := New(100), New(100)
	a.UseShared(cli)
	b.UseShared(cli)
	ha := a.Middleware(30*time.Second, tenantHdr, backend)
	hb := b.Middleware(30*time.Second, tenantHdr, backend)

	if w := get(ha, "t1", "/x?q=1"); w.Header().Get("X-Cache") != "MISS" {
		t.Fatal("replica A first call must miss")
	}
	w := get(hb, "t1", "/x?q=1")
	if w.Header().Get("X-Cache") != "HIT" || n.Load() != 1 || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("replica B must hit A's entry: %s calls=%d", w.Header().Get("X-Cache"), n.Load())
	}
	if w := get(hb, "t2", "/x?q=1"); w.Header().Get("X-Cache") != "MISS" || w.Body.String() == get(hb, "t1", "/x?q=1").Body.String() {
		t.Fatal("tenants must not share entries")
	}
	// a write on replica A invalidates replica B's view
	a.Invalidate("t1")
	if w := get(hb, "t1", "/x?q=1"); w.Header().Get("X-Cache") != "MISS" {
		t.Fatal("invalidation on A must reach B")
	}
	// shared store down: requests still succeed through the local path
	srv.Down = true
	if w := get(ha, "t1", "/y"); w.Code != 200 || w.Body.Len() == 0 {
		t.Fatalf("outage broke the request: %d", w.Code)
	}
}
