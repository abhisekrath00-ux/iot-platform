package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

// Named is a provider in a failover chain with the identity used for its cool-down.
type Named struct {
	ID string
	Provider
}

// Chain tries providers in order and moves to the next only when the current one fails in a way
// another provider could fix: unreachable, timeout, 401/403 (bad key), 408, 429 (quota or rate
// limit), 5xx. A 400, a context-size error or a cancelled run never fails over, and an answer that
// arrived is never retried. A provider that fails over is skipped for a short cool-down so the next
// requests do not wait for it again; the last provider is always tried. Streamed text from a failed
// provider may already be on screen, so the stream path only fails over when nothing was produced.
type Chain struct {
	Providers []Named
	Cooldown  time.Duration // default 60s
}

var (
	coolMu sync.Mutex
	cool   = map[string]time.Time{}
)

// CooldownID makes a cool-down key from endpoint, model and key without keeping the key itself.
func CooldownID(base, model, key string) string {
	h := sha256.Sum256([]byte(base + "\x00" + model + "\x00" + key))
	return hex.EncodeToString(h[:8])
}

// ResetCooldowns clears the failover memory (tests).
func ResetCooldowns() { coolMu.Lock(); cool = map[string]time.Time{}; coolMu.Unlock() }

func (c Chain) Name() string {
	if len(c.Providers) == 0 {
		return ""
	}
	n := c.Providers[0].Name()
	if len(c.Providers) > 1 {
		n += " (+" + itoa(len(c.Providers)-1) + " backup)"
	}
	return n
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}

// Failoverable reports whether another provider could plausibly succeed after err.
func Failoverable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	m := err.Error()
	if strings.Contains(m, "exceeds the available context size") {
		return false
	}
	if strings.Contains(m, "model request failed") {
		return true
	}
	for _, code := range []string{"returned 401", "returned 403", "returned 408", "returned 429", "returned 5"} {
		if strings.Contains(m, code) {
			return true
		}
	}
	return false
}

func (c Chain) order() []Named {
	cd := c.Cooldown
	if cd == 0 {
		cd = 60 * time.Second
	}
	now := time.Now()
	coolMu.Lock()
	defer coolMu.Unlock()
	var live []Named
	for _, p := range c.Providers {
		if until, ok := cool[p.ID]; ok && now.Before(until) {
			continue
		}
		live = append(live, p)
	}
	if len(live) == 0 && len(c.Providers) > 0 { // everything cooling: try the primary anyway
		live = c.Providers[:1]
	}
	return live
}

func (c Chain) mark(id string) {
	cd := c.Cooldown
	if cd == 0 {
		cd = 60 * time.Second
	}
	coolMu.Lock()
	cool[id] = time.Now().Add(cd)
	coolMu.Unlock()
}

func (c Chain) Chat(ctx context.Context, msgs []Message, tools []Tool) (Message, error) {
	var m Message
	var err error
	for _, p := range c.order() {
		m, err = p.Chat(ctx, msgs, tools)
		if err == nil || ctx.Err() != nil || !Failoverable(err) {
			return m, err
		}
		c.mark(p.ID)
	}
	return m, err
}

func (c Chain) ChatStream(ctx context.Context, msgs []Message, tools []Tool, onDelta func(string)) (Message, error) {
	var m Message
	var err error
	for _, p := range c.order() {
		got := false
		m, err = p.ChatStream(ctx, msgs, tools, func(d string) { got = true; onDelta(d) })
		if err == nil || ctx.Err() != nil || !Failoverable(err) || got {
			return m, err
		}
		c.mark(p.ID)
	}
	return m, err
}
