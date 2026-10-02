package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/cmdexec"
)

type fakeWriter struct {
	got  []string
	fail error
}

func (f *fakeWriter) Write(_ context.Context, point string, v float64) error {
	if f.fail != nil {
		return f.fail
	}
	f.got = append(f.got, point)
	return nil
}

func envJSON(id, action, target, params string, now time.Time) []byte {
	return []byte(`{"request_id":"` + id + `","target":"` + target + `","action":"` + action + `","parameters":` + params +
		`,"approved_by":"alice","issued_at":"` + now.Format(time.RFC3339) + `","expires_at":"` + now.Add(5*time.Minute).Format(time.RFC3339) + `","policy_version":"1"}`)
}

// The whole edge path: gate (allowlist, expiry, replay) then actuator then the
// per-device writer.
func TestModbusActuatorThroughGate(t *testing.T) {
	now := time.Now()
	reg := &writerRegistry{}
	fw := &fakeWriter{}
	reg.set("vfd1", fw)
	act := &modbusActuator{reg: reg}
	gate := cmdexec.NewGate([]string{actionModbusWrite})
	ctx := context.Background()

	ack, _ := cmdexec.Handle(ctx, gate, act, envJSON("r1", "modbus.write", "vfd1", `{"point":"setpoint","value":40}`, now), now)
	if ack.State != "acked" || len(fw.got) != 1 {
		t.Fatalf("good write: %+v", ack)
	}
	// replay of the same request id never writes again
	ack, _ = cmdexec.Handle(ctx, gate, act, envJSON("r1", "modbus.write", "vfd1", `{"point":"setpoint","value":40}`, now), now)
	if ack.State != "rejected" || len(fw.got) != 1 {
		t.Fatalf("replay: %+v", ack)
	}
	cases := map[string]string{
		"r2": `{"point":"setpoint"}`,                        // no value
		"r3": `{"point":"setpoint","value":1,"extra":true}`, // unknown field
		"r4": `[1]`,                                         // wrong shape
	}
	for id, params := range cases {
		ack, _ = cmdexec.Handle(ctx, gate, act, envJSON(id, "modbus.write", "vfd1", params, now), now)
		if ack.State != "failed" || len(fw.got) != 1 {
			t.Fatalf("%s: %+v", id, ack)
		}
	}
	// unknown device, no writer registered
	ack, _ = cmdexec.Handle(ctx, gate, act, envJSON("r5", "modbus.write", "ghost", `{"point":"p","value":1}`, now), now)
	if ack.State != "failed" || !strings.Contains(ack.Detail, "not writable") {
		t.Fatalf("ghost: %+v", ack)
	}
	// action not on the gateway allowlist is rejected before the actuator
	ack, _ = cmdexec.Handle(ctx, gate, act, envJSON("r6", "modbus.format", "vfd1", `{}`, now), now)
	if ack.State != "rejected" {
		t.Fatalf("unlisted action: %+v", ack)
	}
	// empty allowlist (the default) denies modbus.write too
	deny := cmdexec.NewGate(nil)
	ack, _ = cmdexec.Handle(ctx, deny, act, envJSON("r7", "modbus.write", "vfd1", `{"point":"setpoint","value":1}`, now), now)
	if ack.State != "rejected" || len(fw.got) != 1 {
		t.Fatalf("default deny: %+v", ack)
	}
	// a device-side error is reported as failed, not acked
	fw.fail = context.DeadlineExceeded
	ack, _ = cmdexec.Handle(ctx, gate, act, envJSON("r8", "modbus.write", "vfd1", `{"point":"setpoint","value":2}`, now), now)
	if ack.State != "failed" {
		t.Fatalf("device error: %+v", ack)
	}
}
