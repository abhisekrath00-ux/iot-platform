package respcache

import (
	"context"
	"errors"
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
