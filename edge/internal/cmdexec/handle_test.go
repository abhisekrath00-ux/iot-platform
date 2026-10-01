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
