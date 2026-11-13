package cmdexec

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// Actuator performs a validated command. Runtime Modbus writes still require
// local command mode, action allowlist and per-device write configuration.
type Actuator interface {
	Execute(ctx context.Context, e *Envelope) (detail string, err error)
}

// Ack is published on t/<tenant>/g/<gateway>/cmd/ack.
type Ack struct {
	RequestID string    `json:"request_id"`
	State     string    `json:"state"` // acked | failed | rejected
	Detail    string    `json:"detail,omitempty"`
	At        time.Time `json:"at"`
}

// Handle validates and executes one command. ok means safe to PUBLISH the ACK,
// not merely that a request ID was parsed. Negative local observations do not
// settle the original request: a duplicate may follow an effect whose ACK was
// lost, and an IO error may follow a write already sent. Keep those observations
// local rather than sending a terminal failed/rejected ACK to older servers.
func Handle(ctx context.Context, g *Gate, a Actuator, payload []byte, now time.Time) (ack Ack, ok bool) {
	started := time.Now()
	ack.At = now
	e, err := g.Check(payload, now)
	if err != nil {
		var probe struct {
			RequestID string `json:"request_id"`
		}
		_ = json.Unmarshal(payload, &probe)
		ack.RequestID, ack.State, ack.Detail = probe.RequestID, "rejected", err.Error()
		return ack, false
	}
	ack.RequestID = e.RequestID
	if a == nil {
		ack.State, ack.Detail = "rejected", "no actuator configured"
		return ack, false
	}
	// Durable admission can wait on storage. Do not start an expired effect
	// merely because the envelope was live before that wait. The actuator gets
	// the remaining lifetime too; driver cancellation cannot undo a sent write.
	if err := ctx.Err(); err != nil {
		ack.State, ack.Detail = "rejected", err.Error()
		return ack, false
	}
	remaining := e.ExpiresAt.Sub(now) - time.Since(started)
	if remaining <= 0 {
		ack.State, ack.Detail = "rejected", ErrExpired.Error()
		return ack, false
	}
	actCtx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	detail, err := a.Execute(actCtx, e)
	if err != nil {
		ack.State, ack.Detail = "failed", err.Error()
		return ack, false
	}
	ack.State, ack.Detail = "acked", detail
	return ack, true
}

// Simulated records commands and touches no hardware. It lets the whole path
// (approve, publish, gate, ack, audit) be exercised end to end safely.
type Simulated struct {
	mu   sync.Mutex
	Seen []Envelope
	Fail map[string]error // action -> error, to test failure paths
}

func (s *Simulated) Execute(_ context.Context, e *Envelope) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err, bad := s.Fail[e.Action]; bad {
		return "", err
	}
	s.Seen = append(s.Seen, *e)
	return "simulated: no hardware actuated", nil
}

var ErrSimulatedFault = errors.New("simulated actuator fault")
