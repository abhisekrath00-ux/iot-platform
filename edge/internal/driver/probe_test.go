package driver

import (
	"context"
	"testing"
	"time"

	"go.bug.st/serial"
)

func openerFor(fp serial.Port, err error) PortOpener {
	return func(string, *serial.Mode) (serial.Port, error) { return fp, err }
}

func probeReq() ProbeRequest {
	return ProbeRequest{SessionID: "sess-1", Port: "/dev/ttyFAKE0", Baud: 9600,
		DataBits: 8, StopBits: 1, Parity: "none", Address: 7,
		Func: 3, Register: 10, Count: 2, Type: "f32", WordOrder: "abcd", TimeoutMs: 2000}
}

func TestRunProbeDecodesValues(t *testing.T) {
	// f32 100.0 = words 0x42C8 0x0000 at registers 10,11
	fp := &fakePort{regs: map[int]uint16{10: 0x42C8, 11: 0x0000}}
	res := RunProbe(context.Background(), openerFor(fp, nil), probeReq())
	if !res.OK {
		t.Fatalf("probe failed: %s", res.Error)
	}
	if len(res.Readings) != 1 || res.Readings[0].Value != 100.0 {
		t.Fatalf("readings: %+v", res.Readings)
	}
	if res.Readings[0].PointID != "r10" {
		t.Fatalf("point id: %+v", res.Readings[0])
	}
	if fp.sawAddr != 7 || fp.sawFn != 3 {
		t.Fatalf("frame: addr %d fn %d", fp.sawAddr, fp.sawFn)
	}
	if res.LatencyMs < 0 || res.FinishedAt.IsZero() {
		t.Fatalf("timing not recorded: %+v", res)
	}
}

func TestRunProbeReportsDeviceFault(t *testing.T) {
	fp := &fakePort{regs: map[int]uint16{}, corrupt: true}
	res := RunProbe(context.Background(), openerFor(fp, nil), probeReq())
	if res.OK || res.Error == "" {
		t.Fatalf("corrupt response should fail: %+v", res)
	}
	if res.SessionID != "sess-1" {
		t.Fatalf("session lost: %+v", res)
	}
}

func TestRunProbeRejectsWriteFunction(t *testing.T) {
	req := probeReq()
	req.Func = 6 // write single register: outside the read-only envelope
	fp := &fakePort{regs: map[int]uint16{10: 1, 11: 2}}
	res := RunProbe(context.Background(), openerFor(fp, nil), req)
	if res.OK {
		t.Fatal("write function accepted")
	}
	if fp.sawFn != 0 {
		t.Fatal("a frame was sent for a rejected probe")
	}
}

func TestRunProbeOpenError(t *testing.T) {
	res := RunProbe(context.Background(), openerFor(nil, errTest), probeReq())
	if res.OK || res.Error == "" {
		t.Fatalf("open error should fail: %+v", res)
	}
}

var errTest = &testErr{}

type testErr struct{}

func (*testErr) Error() string { return "no such port" }

func TestProbeTimeoutBound(t *testing.T) {
	// silent device: read times out instead of hanging the agent
	silent := &silentPort{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req := probeReq()
	req.TimeoutMs = 300
	res := RunProbe(ctx, openerFor(silent, nil), req)
	if res.OK || res.Error == "" {
		t.Fatalf("silent device should fail: %+v", res)
	}
}

// silentPort never answers.
type silentPort struct{ fakePort }

func (s *silentPort) Write(p []byte) (int, error) { return len(p), nil }
func (s *silentPort) Read(p []byte) (int, error)  { time.Sleep(50 * time.Millisecond); return 0, nil }
