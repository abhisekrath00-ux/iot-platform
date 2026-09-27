// Rate limiting and security headers. The limiter is a per-client-IP token
// bucket with a background sweeper; it protects unauthenticated endpoints
// (enrollment claim, SSO login/callback) from brute force and stuffing.
package auth

import (
	"net"
	"net/http"
	"sync"
	"time"
)

type bucket struct {
	tokens float64
	last   time.Time
}

type RateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	rate      float64 // tokens per second
	burst     float64
	now       func() time.Time
	stopSweep chan struct{}
}

// NewRateLimiter allows `burst` immediate requests, then refills at
// `perMinute` tokens per minute per client IP.
func NewRateLimiter(perMinute, burst float64) *RateLimiter {
	rl := &RateLimiter{
		buckets: map[string]*bucket{}, rate: perMinute / 60, burst: burst,
		now: time.Now, stopSweep: make(chan struct{}),
	}
	go func() { // evict idle buckets so memory stays bounded
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-rl.stopSweep:
				return
			case <-t.C:
				rl.mu.Lock()
				for ip, b := range rl.buckets {
					if rl.now().Sub(b.last) > 30*time.Minute {
						delete(rl.buckets, ip)
					}
				}
				rl.mu.Unlock()
			}
		}
	}()
	return rl
}

func (rl *RateLimiter) Close() { close(rl.stopSweep) }

func (rl *RateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	b := rl.buckets[ip]
	if b == nil {
		b = &bucket{tokens: rl.burst, last: rl.now()}
		rl.buckets[ip] = b
	}
	elapsed := rl.now().Sub(b.last).Seconds()
	b.last = rl.now()
	b.tokens += elapsed * rl.rate
	if b.tokens > rl.burst {
		b.tokens = rl.burst
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Middleware rejects with 429 once the client's bucket is empty.
// X-Forwarded-For is deliberately not trusted: behind a real proxy, set
// TrustedProxyHeaders separately rather than letting clients spoof IPs.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		if !rl.allow(host) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// SecurityHeaders sets defense-in-depth headers on every response.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
