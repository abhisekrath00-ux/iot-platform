// Package cmdexec is the edge-side gate for server commands. It decides
// whether a received command envelope may be executed; it does not actuate
// anything. Wiring to drivers is deliberately not done yet (see docs/security.md).
package cmdexec

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

type Envelope struct {
	RequestID     string          `json:"request_id"`
	Target        string          `json:"target"`
	Action        string          `json:"action"`
	Parameters    json.RawMessage `json:"parameters"`
	ApprovedBy    string          `json:"approved_by"`
	IssuedAt      time.Time       `json:"issued_at"`
	ExpiresAt     time.Time       `json:"expires_at"`
	PolicyVersion string          `json:"policy_version"`
}

var (
	ErrMalformed = errors.New("malformed command envelope")
	ErrExpired   = errors.New("command expired")
	ErrNotYet    = errors.New("command issued in the future")
	ErrDenied    = errors.New("action not in gateway allowlist")
	ErrReplay    = errors.New("command already seen")
	ErrTTL       = errors.New("command lifetime exceeds maximum")
)

// Gate validates envelopes. Zero allowlist denies everything (fail closed).
type Gate struct {
	Allowed []string
	MaxTTL  time.Duration // longest accepted expires_at - issued_at
	Skew    time.Duration // tolerated clock skew on issued_at

	mu   sync.Mutex
	seen map[string]time.Time
}

func NewGate(allowed []string) *Gate {
	return &Gate{Allowed: allowed, MaxTTL: 10 * time.Minute, Skew: 30 * time.Second, seen: map[string]time.Time{}}
}

// Check parses and validates a payload. A request_id is accepted at most once
// until its expiry has passed; non-idempotent actuation is never replayed.
func (g *Gate) Check(payload []byte, now time.Time) (*Envelope, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for id, exp := range g.seen { // bounded: entries die at expiry
		if !now.Before(exp) {
			delete(g.seen, id)
		}
	}
	var e Envelope
	dec := json.NewDecoder(bytesReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if e.RequestID == "" || e.Target == "" || e.Action == "" || e.ApprovedBy == "" || e.IssuedAt.IsZero() || e.ExpiresAt.IsZero() {
		return nil, ErrMalformed
	}
	if !now.Before(e.ExpiresAt) {
		return nil, ErrExpired
	}
	if e.IssuedAt.After(now.Add(g.Skew)) {
		return nil, ErrNotYet
	}
	if e.ExpiresAt.Sub(e.IssuedAt) > g.MaxTTL {
		return nil, ErrTTL
	}
	allowed := false
	for _, a := range g.Allowed {
		if a == e.Action {
			allowed = true
		}
	}
	if !allowed {
		return nil, ErrDenied
	}
	if _, dup := g.seen[e.RequestID]; dup {
		return nil, ErrReplay
	}
	g.seen[e.RequestID] = e.ExpiresAt
	return &e, nil
}

type reader struct {
	b []byte
	i int
}

func (r *reader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, errEOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}

var errEOF = io.EOF

func bytesReader(b []byte) *reader { return &reader{b: b} }
