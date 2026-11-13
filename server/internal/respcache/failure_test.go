package respcache

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type failedShared struct{}

func (failedShared) Get(string) (string, bool, error)        { return "", false, errors.New("down") }
func (failedShared) Set(string, string, time.Duration) error { return errors.New("down") }
func (failedShared) Incr(string) (int64, error)              { return 0, errors.New("down") }
func (failedShared) GetInt(string) (int64, error)            { return 0, errors.New("down") }

func TestSharedReadFailureBypassesLocalCache(t *testing.T) {
	c := New(10)
	var value atomic.Int32
	value.Store(1)
	var calls atomic.Int32
	h := c.Middleware(time.Minute, tenantHdr, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.Write([]byte{byte('0' + value.Load())}) })
	// Seed a local cached value before enabling the shared store. It must not leak
	// through on outage, and failed reads must not create fresh local entries.
	if get(h, "a", "/x").Body.String() != "1" {
		t.Fatal("seed")
	}
	c.UseShared(failedShared{})
	for _, n := range []int32{2, 3} {
		value.Store(n)
		if w := get(h, "a", "/x"); w.Body.String() != string(rune('0'+n)) || w.Header().Get("X-Cache") != "" {
			t.Fatal("served cached result during shared outage", w.Body.String())
		}
	}
	if calls.Load() != 3 {
		t.Fatal("outage did not read backend each time")
	}
}

func TestUncacheableHeadersAndOversizePreserved(t *testing.T) {
	for _, shared := range []bool{false, true} {
		for _, header := range []string{"Cache-Control:private", "Cache-Control:no-store", "Cache-Control:public, No-Cache=\"field\"", "Set-Cookie:session=x", "Vary:Accept-Language", "WWW-Authenticate:Basic", "Location:/next", "Content-Disposition:attachment", "oversize:"} {
			t.Run(header+map[bool]string{false: "/local", true: "/shared"}[shared], func(t *testing.T) {
				c := New(2)
				if shared {
					c.UseShared(&memoryShared{m: map[string]string{}})
				}
				var calls int
				h := c.Middleware(time.Minute, tenantHdr, func(w http.ResponseWriter, r *http.Request) {
					calls++
					for i := range header {
						if header[i] == ':' {
							if i > 0 && header[:i] != "oversize" {
								w.Header().Set(header[:i], header[i+1:])
							}
							break
						}
					}
					if header == "oversize:" {
						w.Write(make([]byte, maxSharedBody+1))
					} else {
						w.Write([]byte("body"))
					}
				})
				a, b := get(h, "a", "/x"), get(h, "a", "/x")
				if calls != 2 || a.Header().Get("X-Cache") != "" || b.Body.Len() != a.Body.Len() {
					t.Fatal("uncacheable response stored")
				}
				for k, v := range a.Header() {
					if b.Header().Get(k) != v[0] {
						t.Fatal("header lost", k)
					}
				}
			})
		}
	}
}

// This store is just a deterministic hit/miss fixture, not Valkey proof.
type memoryShared struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memoryShared) Get(k string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[k]
	return v, ok, nil
}
func (s *memoryShared) Set(k, v string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = v
	return nil
}
func (s *memoryShared) Incr(string) (int64, error)   { return 1, nil }
func (s *memoryShared) GetInt(string) (int64, error) { return 0, nil }

func TestCancelledWaiterDoesNotHangOrRunBackend(t *testing.T) {
	for _, shared := range []bool{false, true} {
		c := New(10)
		if shared {
			c.UseShared(&memoryShared{m: map[string]string{}})
		}
		entered, release, leaderDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var calls atomic.Int32
		h := c.Middleware(time.Minute, tenantHdr, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			close(entered)
			<-release
			w.Write([]byte("ok"))
		})
		go func() { get(h, "a", "/x"); close(leaderDone) }()
		<-entered
		ctx, cancel := context.WithCancel(context.Background())
		r := httptest.NewRequest("GET", "/x", nil).WithContext(ctx)
		r.Header.Set("T", "a")
		done := make(chan struct{})
		go func() { h(httptest.NewRecorder(), r); close(done) }()
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("cancelled waiter stuck")
		}
		close(release)
		<-leaderDone
		if calls.Load() != 1 {
			t.Fatal("waiter ran backend")
		}
	}
}

// A net/http server recovers handler panics. The cache must still release the
// single-flight generation; otherwise later readers wait on a channel that no
// surviving leader can close. Partial output must never become a cached hit.
func TestPanickingLeaderReleasesFlightWithoutCachingPartialBody(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "shared"}[shared], func(t *testing.T) {
			c := New(10)
			if shared {
				c.UseShared(&memoryShared{m: map[string]string{}})
			}
			var calls int
			var flight *call
			h := c.Middleware(time.Minute, tenantHdr, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					c.mu.Lock()
					for _, f := range c.inflight {
						flight = f
					}
					c.mu.Unlock()
					w.Write([]byte("partial"))
					panic("backend failure")
				}
				w.Write([]byte("fresh"))
			})
			func() {
				defer func() {
					if recover() != "backend failure" {
						t.Fatal("cache swallowed or changed handler panic")
					}
				}()
				get(h, "a", "/panic")
			}()
			if flight == nil {
				t.Fatal("leader did not register a flight")
			}
			select {
			case <-flight.done:
			default:
				t.Fatal("panicked leader left its readers blocked")
			}
			c.mu.Lock()
			pending := len(c.inflight)
			c.mu.Unlock()
			if pending != 0 || flight.ent != nil {
				t.Fatal("panicked leader retained a flight or partial response")
			}
			if w := get(h, "a", "/panic"); w.Body.String() != "fresh" || w.Header().Get("X-Cache") != "MISS" {
				t.Fatal("next reader did not recompute fresh data", w.Body.String())
			}
			if w := get(h, "a", "/panic"); w.Body.String() != "fresh" || w.Header().Get("X-Cache") != "HIT" || calls != 2 {
				t.Fatal("successful retry did not settle a cache entry")
			}
		})
	}
}

func TestHTTPServerRemainsReadableAfterCachedHandlerPanic(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "shared"}[shared], func(t *testing.T) {
			c := New(10)
			if shared {
				c.UseShared(&memoryShared{m: map[string]string{}})
			}
			var calls atomic.Int32
			h := c.Middleware(time.Minute, func(*http.Request) string { return "test-tenant" }, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					w.Write([]byte("partial"))
					panic("injected backend failure")
				}
				w.Write([]byte("fresh"))
			})
			srv := httptest.NewServer(h)
			defer srv.Close()
			client := srv.Client()
			client.Timeout = time.Second
			if response, err := client.Get(srv.URL); err == nil {
				response.Body.Close()
				t.Fatal("panicking request unexpectedly produced a successful response")
			}
			for _, want := range []string{"MISS", "HIT"} {
				response, err := client.Get(srv.URL)
				if err != nil {
					t.Fatal("HTTP server remained poisoned after recovering a handler panic", err)
				}
				body, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil || response.StatusCode != 200 || string(body) != "fresh" || response.Header.Get("X-Cache") != want {
					t.Fatal("wrong fresh response after panic", string(body), response.Header)
				}
			}
			if calls.Load() != 2 {
				t.Fatal("cache recovery lost normal single-flight/hit behavior")
			}
		})
	}
}

// Failure signals must distinguish read bypass from failed remote invalidation:
// the latter can leave another replica's response stale until its TTL.
type partialFailureShared struct {
	*memoryShared
	fail string
}

func (s partialFailureShared) GetInt(k string) (int64, error) {
	if s.fail == "generation" {
		return -1, nil
	}
	return s.memoryShared.GetInt(k)
}
func (s partialFailureShared) Get(k string) (string, bool, error) {
	if s.fail == "read" {
		return "", false, errors.New("read failed")
	}
	return s.memoryShared.Get(k)
}
func (s partialFailureShared) Set(k, v string, d time.Duration) error {
	if s.fail == "write" {
		return errors.New("write failed")
	}
	return s.memoryShared.Set(k, v, d)
}
func (s partialFailureShared) Incr(k string) (int64, error) {
	if s.fail == "invalidate" {
		return 0, errors.New("invalidate failed")
	}
	return s.memoryShared.Incr(k)
}
func TestSharedFailureCounters(t *testing.T) {
	for _, phase := range []string{"generation", "read", "write", "invalidate"} {
		t.Run(phase, func(t *testing.T) {
			c := New(10)
			c.UseShared(partialFailureShared{&memoryShared{m: map[string]string{}}, phase})
			h := c.Middleware(time.Minute, tenantHdr, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("fresh")) })
			if phase == "invalidate" {
				c.Invalidate("a")
			} else if get(h, "a", "/x").Body.String() != "fresh" {
				t.Fatal("failure changed backend response")
			}
			reads, writes, invalidations := uint64(0), uint64(0), uint64(0)
			switch phase {
			case "generation", "read":
				reads = 1
			case "write":
				writes = 1
			case "invalidate":
				invalidations = 1
			}
			if c.SharedReadFailures.Load() != reads || c.SharedWriteFailures.Load() != writes || c.SharedInvalidationFailures.Load() != invalidations {
				t.Fatalf("wrong failure counters for %s", phase)
			}
		})
	}
}
