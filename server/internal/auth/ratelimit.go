// Rate limiting and security headers. The limiter is a per-client-IP token
// bucket with a background sweeper; it protects unauthenticated endpoints
// (enrollment claim, SSO login/callback) from brute force and stuffing.
package auth

import (
	"net"
	"net/http"
	"os"
	"strings"
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
// X-Forwarded-For is not trusted unless the connection comes from a proxy listed in TRUSTED_PROXIES
// (see ClientIP). Without that, behind a reverse proxy every user shares the proxy's address and therefore one
// bucket, so one person could lock everyone out of sign-in.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.allow(ClientIP(r)) {
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

// trustedProxies is read once from TRUSTED_PROXIES: comma separated CIDRs or addresses of reverse proxies that
// set X-Forwarded-For themselves (the bundled nginx does, overwriting any client-supplied value). Empty by
// default, so a directly exposed API never believes the header.
var trustedProxies = ParseTrustedProxies(os.Getenv("TRUSTED_PROXIES"))

// ParseTrustedProxies ignores entries that are not a CIDR or an IP.
func ParseTrustedProxies(v string) []*net.IPNet {
	var out []*net.IPNet
	for _, f := range strings.Split(v, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !strings.Contains(f, "/") {
			if ip := net.ParseIP(f); ip != nil {
				if ip.To4() != nil {
					f += "/32"
				} else {
					f += "/128"
				}
			}
		}
		if _, n, err := net.ParseCIDR(f); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// ClientIP is the address used for per-client limits. The connection's address is used unless it belongs to a
// trusted proxy; then X-Forwarded-For is walked from the right and the first address that is not itself a
// trusted proxy wins (an attacker can only prepend entries, never replace the one the proxy added).
func ClientIP(r *http.Request) string { return clientIP(r, trustedProxies) }

func clientIP(r *http.Request, trusted []*net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if len(trusted) == 0 || !inNets(host, trusted) {
		return host
	}
	parts := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		p := strings.TrimSpace(parts[i])
		if net.ParseIP(p) == nil {
			return host // malformed: do not guess
		}
		if !inNets(p, trusted) {
			return p
		}
	}
	return host
}

func inNets(h string, nets []*net.IPNet) bool {
	ip := net.ParseIP(h)
	if ip == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
