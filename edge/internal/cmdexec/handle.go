package cmdexec

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// Actuator performs a validated command against hardware. Real drivers will
// implement this only after the safety gating in docs/security.md is met.
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

// Handle validates a payload and, if it passes the gate, executes it. A nil
// Actuator rejects every command (fail closed). The returned ack is always
// publishable; ok is false only when the payload had no usable request_id, so
// there is nothing the server could match an ack to.
func Handle(ctx context.Context, g *Gate, a Actuator, payload []byte, now time.Time) (ack Ack, ok bool) {
	ack.At = now
	e, err := g.Check(payload, now)
	if err != nil {
		var probe struct {
			RequestID string `json:"request_id"`
		}
		_ = json.Unmarshal(payload, &probe)
		ack.RequestID, ack.State, ack.Detail = probe.RequestID, "rejected", err.Error()
		return ack, probe.RequestID != ""
	}
	ack.RequestID = e.RequestID
	if a == nil {
		ack.State, ack.Detail = "rejected", "no actuator configured"
		return ack, true
	}
	detail, err := a.Execute(ctx, e)
	if err != nil {
		ack.State, ack.Detail = "failed", err.Error()
		return ack, true
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
