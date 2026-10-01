package auth

import (
	"sync"
	"time"
)

// Limiter is a per-principal token keyBucket, in memory and per replica (so the
// effective cluster limit is rate x replicas). It bounds a leaked or runaway
// API key; it is not a substitute for an edge/WAF rate limit.
type Limiter struct {
	rate  float64 // tokens per second
	burst float64
	mu    sync.Mutex
	b     map[string]*keyBucket
	now   func() time.Time
}

type keyBucket struct {
	tokens float64
	last   time.Time
}

func NewLimiter(perMinute, burst int) *Limiter {
	return &Limiter{rate: float64(perMinute) / 60, burst: float64(burst), b: map[string]*keyBucket{}, now: time.Now}
}

// Allow takes one token for key. When it refuses, retryAfter is how long until one is available.
func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.now()
	bk := l.b[key]
	if bk == nil {
		if len(l.b) > 10000 { // bound memory: drop idle buckets
			for k, v := range l.b {
				if t.Sub(v.last) > time.Hour {
					delete(l.b, k)
				}
			}
		}
		bk = &keyBucket{tokens: l.burst, last: t}
		l.b[key] = bk
	}
	bk.tokens += t.Sub(bk.last).Seconds() * l.rate
	if bk.tokens > l.burst {
		bk.tokens = l.burst
	}
	bk.last = t
	if bk.tokens >= 1 {
		bk.tokens--
		return true, 0
	}
	return false, time.Duration((1 - bk.tokens) / l.rate * float64(time.Second))
}

var keyLimiter *Limiter

// SetKeyLimiter enables rate limiting for API-key requests (nil disables).
func SetKeyLimiter(l *Limiter) { keyLimiter = l }
