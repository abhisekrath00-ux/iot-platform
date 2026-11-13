// Package respcache is a small in-process response cache for hot, read-only
// API endpoints (fleet overview, latest values, point lists).
//
// Design limits, stated honestly:
//   - Per process. With N API replicas a client may see data up to ttl old
//     from any replica; TTLs are therefore short (seconds), not minutes.
//   - Keys are tenant-scoped; only cacheable GET 200 bodies up to 1 MiB are stored.
//   - A per-tenant generation counter lets mutating handlers invalidate a
//     tenant's entries in O(1).
//   - Concurrent misses for one key share one backend call (no stampede).
//   - Bounded: at most maxEntries responses; expired entries are dropped first.
package respcache

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type entry struct {
	body    []byte
	ctype   string
	expires time.Time
}

type call struct {
	done chan struct{}
	ent  *entry
}

// Shared is an optional backing store (Redis) that makes the cache and its
// invalidation visible to every API replica. On read errors the request bypasses
// caching and reads its backend directly; it does not consult a local stale copy.
type Shared interface {
	Get(key string) (string, bool, error)
	Set(key, val string, ttl time.Duration) error
	Incr(key string) (int64, error)
	GetInt(key string) (int64, error)
}

// maxSharedBody bounds what is written to the shared store.
const maxSharedBody = 1 << 20

// Cache is safe for concurrent use.
type Cache struct {
	mu         sync.Mutex
	m          map[string]*entry
	gens       map[string]uint64
	inflight   map[string]*call
	maxEntries int
	now        func() time.Time
	shared     Shared

	Hits, Misses, Invalidations atomic.Uint64
	// Aggregate process counters only: no tenant, key, URI or credential labels.
	SharedReadFailures, SharedWriteFailures, SharedInvalidationFailures atomic.Uint64
}

func New(maxEntries int) *Cache {
	return &Cache{m: map[string]*entry{}, gens: map[string]uint64{}, inflight: map[string]*call{},
		maxEntries: maxEntries, now: time.Now}
}

// UseShared switches the cache to a store shared by all replicas.
func (c *Cache) UseShared(s Shared) { c.shared = s }

// Invalidate drops every cached response for a tenant.
func (c *Cache) Invalidate(tenant string) {
	if c.shared != nil {
		// If this fails, other replicas keep serving until the entry TTL (seconds).
		if _, err := c.shared.Incr("hx:gen:" + tenant); err != nil {
			c.SharedInvalidationFailures.Add(1)
		}
	}
	c.mu.Lock()
	c.gens[tenant]++
	c.mu.Unlock()
	c.Invalidations.Add(1)
}

func (c *Cache) key(tenant, uri string) string {
	return tenant + "\x00" + uitoa(c.gens[tenant]) + "\x00" + uri
}

func uitoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

// Middleware caches GET responses of next for ttl. tenantOf must return the
// authenticated tenant; an empty tenant bypasses the cache (never share).
func (c *Cache) Middleware(ttl time.Duration, tenantOf func(*http.Request) string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenant := tenantOf(r)
		if r.Method != http.MethodGet || tenant == "" || ttl <= 0 {
			next(w, r)
			return
		}
		if c.shared != nil {
			if !c.serveShared(w, r, tenant, ttl, next) {
				next(w, r)
			}
			return
		}
		c.mu.Lock()
		k := c.key(tenant, r.URL.RequestURI())
		if e, ok := c.m[k]; ok && c.now().Before(e.expires) {
			c.mu.Unlock()
			c.Hits.Add(1)
			write(w, e, "HIT")
			return
		}
		if f, ok := c.inflight[k]; ok {
			c.mu.Unlock()
			select {
			case <-f.done:
			case <-r.Context().Done():
				return
			}
			if f.ent != nil {
				c.Hits.Add(1)
				write(w, f.ent, "HIT")
				return
			}
			next(w, r) // leader failed or was not cacheable: serve fresh
			return
		}
		f := &call{done: make(chan struct{})}
		c.inflight[k] = f
		c.mu.Unlock()
		defer c.abandonFlight(k, f)
		c.Misses.Add(1)

		rec := httptest.NewRecorder()
		next(rec, r)
		var ent *entry
		if cacheable(rec) {
			ent = &entry{body: rec.Body.Bytes(), ctype: rec.Header().Get("Content-Type"), expires: c.now().Add(ttl)}
		}
		c.mu.Lock()
		delete(c.inflight, k)
		if ent != nil {
			c.evictLocked()
			c.m[k] = ent
		}
		f.ent = ent
		c.mu.Unlock()
		close(f.done)

		if ent != nil {
			write(w, ent, "MISS")
			return
		}
		for h, v := range rec.Header() {
			w.Header()[h] = v
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	}
}

// abandonFlight runs on panic or Goexit as well as normal return. A successful
// leader already removed its flight and published its result. On failure, unblock
// readers without caching a partial body, while preserving the original panic.
func (c *Cache) abandonFlight(key string, f *call) {
	c.mu.Lock()
	if c.inflight[key] == f {
		delete(c.inflight, key)
		close(f.done)
	}
	c.mu.Unlock()
}

func write(w http.ResponseWriter, e *entry, state string) {
	if e.ctype != "" {
		w.Header().Set("Content-Type", e.ctype)
	}
	w.Header().Set("X-Cache", state)
	_, _ = w.Write(e.body)
}

func (c *Cache) evictLocked() {
	if len(c.m) < c.maxEntries {
		return
	}
	now := c.now()
	for k, e := range c.m {
		if !now.Before(e.expires) {
			delete(c.m, k)
		}
	}
	// Still full (or entries from stale generations linger): drop arbitrary
	// entries until there is room. Correctness does not depend on which.
	for k := range c.m {
		if len(c.m) < c.maxEntries {
			return
		}
		delete(c.m, k)
	}
}

// serveShared handles one request through the shared store. It returns false
// when the store cannot be read, so the caller reads the backend without caching.
func (c *Cache) serveShared(w http.ResponseWriter, r *http.Request, tenant string, ttl time.Duration, next http.HandlerFunc) bool {
	gen, err := c.shared.GetInt("hx:gen:" + tenant)
	if err != nil || gen < 0 {
		c.SharedReadFailures.Add(1)
		return false
	}
	sum := sha256.Sum256([]byte(r.URL.RequestURI()))
	k := "hx:rc:" + tenant + ":" + uitoa(uint64(gen)) + ":" + hex.EncodeToString(sum[:16])
	if v, ok, err := c.shared.Get(k); err != nil {
		c.SharedReadFailures.Add(1)
		return false
	} else if ok {
		if i := indexNUL(v); i >= 0 {
			c.Hits.Add(1)
			write(w, &entry{ctype: v[:i], body: []byte(v[i+1:])}, "HIT")
			return true
		}
	}
	c.mu.Lock()
	if f, ok := c.inflight[k]; ok {
		c.mu.Unlock()
		select {
		case <-f.done:
		case <-r.Context().Done():
			return true
		}
		if f.ent != nil {
			c.Hits.Add(1)
			write(w, f.ent, "HIT")
			return true
		}
		next(w, r)
		return true
	}
	f := &call{done: make(chan struct{})}
	c.inflight[k] = f
	c.mu.Unlock()
	defer c.abandonFlight(k, f)
	c.Misses.Add(1)

	rec := httptest.NewRecorder()
	next(rec, r)
	var ent *entry
	if cacheable(rec) {
		ent = &entry{body: rec.Body.Bytes(), ctype: rec.Header().Get("Content-Type"), expires: c.now().Add(ttl)}
		if len(ent.body) <= maxSharedBody {
			if err := c.shared.Set(k, ent.ctype+"\x00"+string(ent.body), ttl); err != nil {
				c.SharedWriteFailures.Add(1)
			}
		}
	}
	c.mu.Lock()
	delete(c.inflight, k)
	f.ent = ent
	c.mu.Unlock()
	close(f.done)
	if ent != nil {
		write(w, ent, "MISS")
		return true
	}
	for h, v := range rec.Header() {
		w.Header()[h] = v
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
	return true
}

func indexNUL(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return i
		}
	}
	return -1
}

// A cached response only preserves content type and body. Refuse headers whose
// semantics would be lost, personalized cookies, and all Vary responses rather
// than risk sharing a representation under an incomplete key.
func cacheable(rec *httptest.ResponseRecorder) bool {
	if rec.Code != http.StatusOK || rec.Body.Len() > maxSharedBody {
		return false
	}
	for key, values := range rec.Header() {
		switch strings.ToLower(key) {
		case "set-cookie", "vary", "www-authenticate", "location", "content-disposition":
			return false
		case "cache-control":
			for _, v := range values {
				for _, directive := range strings.Split(v, ",") {
					name := strings.TrimSpace(strings.SplitN(strings.ToLower(directive), "=", 2)[0])
					if name == "private" || name == "no-store" || name == "no-cache" {
						return false
					}
				}
			}
		}
	}
	return true
}
