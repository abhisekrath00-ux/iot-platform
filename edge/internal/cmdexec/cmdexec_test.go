package cmdexec

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC)

func env(mut func(*Envelope)) []byte {
	e := Envelope{RequestID: "r1", Target: "door-1", Action: "unlock", Parameters: json.RawMessage(`{}`),
		ApprovedBy: "u2", IssuedAt: t0, ExpiresAt: t0.Add(5 * time.Minute), PolicyVersion: "v1"}
	if mut != nil {
		mut(&e)
	}
	b, _ := json.Marshal(e)
	return b
}

func TestCheck(t *testing.T) {
	cases := []struct {
		name    string
		allowed []string
		payload []byte
		now     time.Time
		want    error
	}{
		{"ok", []string{"unlock"}, env(nil), t0.Add(time.Second), nil},
		{"empty allowlist fails closed", nil, env(nil), t0, ErrDenied},
		{"not allowed", []string{"lock"}, env(nil), t0, ErrDenied},
		{"expired", []string{"unlock"}, env(nil), t0.Add(5 * time.Minute), ErrExpired},
		{"future issue", []string{"unlock"}, env(func(e *Envelope) { e.IssuedAt = t0.Add(time.Hour); e.ExpiresAt = t0.Add(time.Hour + time.Minute) }), t0, ErrNotYet},
		{"ttl too long", []string{"unlock"}, env(func(e *Envelope) { e.ExpiresAt = t0.Add(time.Hour) }), t0, ErrTTL},
		{"missing approver", []string{"unlock"}, env(func(e *Envelope) { e.ApprovedBy = "" }), t0, ErrMalformed},
		{"unknown field", []string{"unlock"}, []byte(`{"request_id":"x","evil":1}`), t0, ErrMalformed},
		{"junk", []string{"unlock"}, []byte(`nope`), t0, ErrMalformed},
	}
	for _, c := range cases {
		_, err := NewGate(c.allowed).Check(c.payload, c.now)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
	}
}

func TestReplayRejectedThenForgottenAfterExpiry(t *testing.T) {
	g := NewGate([]string{"unlock"})
	if _, err := g.Check(env(nil), t0); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Check(env(nil), t0.Add(time.Second)); !errors.Is(err, ErrReplay) {
		t.Fatalf("want replay, got %v", err)
	}
	// After expiry the id is pruned; an expired envelope is still refused as expired, not stored.
	if _, err := g.Check(env(nil), t0.Add(6*time.Minute)); !errors.Is(err, ErrExpired) {
		t.Fatalf("want expired, got %v", err)
	}
	if len(g.seen) != 0 {
		t.Fatalf("seen not pruned: %d", len(g.seen))
	}
}
