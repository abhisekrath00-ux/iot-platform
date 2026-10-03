package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/cmdexec"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
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

func autoEnv(id, target, params string, now time.Time, ttl time.Duration) []byte {
	return []byte(`{"request_id":"` + id + `","target":"` + target + `","action":"modbus.write","parameters":` + params +
		`,"approved_by":"flow-auto:t1","mode":"automatic","issued_at":"` + now.Format(time.RFC3339) + `","expires_at":"` + now.Add(ttl).Format(time.RFC3339) + `","policy_version":"1"}`)
}

// The edge is the last gate for commands nobody approved: opt-in in the local file, a configured
// alarm output, on or off only, and a short life.
func TestAutomaticCommandsAreGatedAtTheEdge(t *testing.T) {
	now := time.Now()
	ctx := context.Background()
	outputs := []config.Output{{Name: "siren", Class: "alarm", Kind: "modbus_coil", Device: "io1", Point: "relay1"}}
	run := func(allow bool, id, target, params string, ttl time.Duration) (cmdexec.Ack, *fakeWriter) {
		reg := &writerRegistry{}
		fw := &fakeWriter{}
		reg.set("io1", fw)
		reg.set("vfd1", fw)
		act := &modbusActuator{reg: reg, auto: autoAllowed(allow, outputs)}
		ack, _ := cmdexec.Handle(ctx, cmdexec.NewGate([]string{actionModbusWrite}), act, autoEnv(id, target, params, now, ttl), now)
		return ack, fw
	}
	if ack, fw := run(true, "a1", "io1", `{"point":"relay1","value":1}`, 30*time.Second); ack.State != "acked" || len(fw.got) != 1 {
		t.Fatalf("allowed alarm output: %+v", ack)
	}
	for name, c := range map[string]struct {
		allow  bool
		target string
		params string
		ttl    time.Duration
	}{
		"not opted in locally":      {false, "io1", `{"point":"relay1","value":1}`, 30 * time.Second},
		"not an alarm output":       {true, "vfd1", `{"point":"setpoint","value":1}`, 30 * time.Second},
		"other point on the device": {true, "io1", `{"point":"relay2","value":1}`, 30 * time.Second},
		"not on or off":             {true, "io1", `{"point":"relay1","value":0.5}`, 30 * time.Second},
		"lives too long":            {true, "io1", `{"point":"relay1","value":1}`, 5 * time.Minute},
	} {
		ack, fw := run(c.allow, "b-"+name, c.target, c.params, c.ttl)
		if ack.State != "rejected" && ack.State != "failed" || len(fw.got) != 0 {
			t.Errorf("%s: %+v wrote=%v", name, ack, fw.got)
		}
	}
	// a human-approved command is unchanged and does not depend on the automatic opt-in
	reg := &writerRegistry{}
	fw := &fakeWriter{}
	reg.set("vfd1", fw)
	act := &modbusActuator{reg: reg, auto: nil}
	ack, _ := cmdexec.Handle(ctx, cmdexec.NewGate([]string{actionModbusWrite}), act, envJSON("h1", "modbus.write", "vfd1", `{"point":"setpoint","value":40}`, now), now)
	if ack.State != "acked" {
		t.Fatalf("approved command: %+v", ack)
	}
	// an unknown mode is malformed
	bad := strings.Replace(string(autoEnv("m1", "io1", `{"point":"relay1","value":1}`, now, 30*time.Second)), `"automatic"`, `"yolo"`, 1)
	if ack, _ := cmdexec.Handle(ctx, cmdexec.NewGate([]string{actionModbusWrite}), act, []byte(bad), now); ack.State != "rejected" {
		t.Fatalf("unknown mode: %+v", ack)
	}
}
