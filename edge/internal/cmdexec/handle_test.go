package cmdexec

import (
	"context"
	"testing"
	"time"
)

func TestHandleStates(t *testing.T) {
	ctx := context.Background()
	now := t0.Add(time.Second)
	sim := &Simulated{Fail: map[string]error{"boom": ErrSimulatedFault}}

	g := NewGate([]string{"unlock", "boom"})
	ack, ok := Handle(ctx, g, sim, env(nil), now)
	if !ok || ack.State != "acked" || ack.RequestID != "r1" || len(sim.Seen) != 1 {
		t.Fatalf("ok path: %+v seen=%d", ack, len(sim.Seen))
	}
	// replay is rejected and not executed twice
	ack, _ = Handle(ctx, g, sim, env(nil), now)
	if ack.State != "rejected" || len(sim.Seen) != 1 {
		t.Fatalf("replay: %+v seen=%d", ack, len(sim.Seen))
	}
	ack, _ = Handle(ctx, g, sim, env(func(e *Envelope) { e.RequestID = "r2"; e.Action = "boom" }), now)
	if ack.State != "failed" {
		t.Fatalf("fault: %+v", ack)
	}
	ack, _ = Handle(ctx, g, sim, env(func(e *Envelope) { e.RequestID = "r3"; e.Action = "format" }), now)
	if ack.State != "rejected" {
		t.Fatalf("not allowed: %+v", ack)
	}
	// nil actuator fails closed
	ack, _ = Handle(ctx, NewGate([]string{"unlock"}), nil, env(func(e *Envelope) { e.RequestID = "r4" }), now)
	if ack.State != "rejected" || ack.Detail != "no actuator configured" {
		t.Fatalf("nil actuator: %+v", ack)
	}
	// unusable payload: nothing to ack
	if _, ok := Handle(ctx, g, sim, []byte("junk"), now); ok {
		t.Fatal("junk payload should not produce an ack")
	}
}

// A negative observation about this delivery must not falsely settle the
// original request after a lost ACK or a partial physical IO failure.
func TestNegativeCommandObservationsAreNotPublishable(t *testing.T) {
	now := t0.Add(time.Second)
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		gate    *Gate
		a       Actuator
		payload []byte
	}{
		{"not allowed", NewGate(nil), &Simulated{}, env(nil)},
		{"no actuator", NewGate([]string{"unlock"}), nil, env(nil)},
		{"expired", NewGate([]string{"unlock"}), &Simulated{}, env(func(e *Envelope) { e.ExpiresAt = t0 })},
		{"IO error", NewGate([]string{"unlock"}), &Simulated{Fail: map[string]error{"unlock": ErrSimulatedFault}}, env(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ack, ok := Handle(ctx, tc.gate, tc.a, tc.payload, now)
			if ok || ack.State == "acked" {
				t.Fatalf("publishable negative %+v ok%v", ack, ok)
			}
		})
	}
	g := NewGate([]string{"unlock"})
	a := &Simulated{}
	Handle(ctx, g, a, env(nil), now)
	ack, ok := Handle(ctx, g, a, env(nil), now)
	if ok || ack.State != "rejected" || len(a.Seen) != 1 {
		t.Fatalf("replay %+v ok%v", ack, ok)
	}
}
