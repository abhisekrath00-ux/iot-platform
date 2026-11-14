package driver

import (
	"context"
	"fmt"
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

func TestRunProbeWindowsCOMPreservesSerialMode(t *testing.T) {
	req := probeReq()
	req.Port = "COM7"
	req.Parity = "even"
	fp := &fakePort{regs: map[int]uint16{10: 0x42c8, 11: 0}}
	called := false
	res := RunProbe(context.Background(), func(port string, mode *serial.Mode) (serial.Port, error) {
		called = true
		if port != "COM7" || mode.BaudRate != 9600 || mode.DataBits != 8 || mode.Parity != serial.EvenParity || mode.StopBits != serial.OneStopBit {
			t.Fatalf("unexpected port or mode %s %+v", port, mode)
		}
		return fp, nil
	}, req)
	if !called || !res.OK || len(res.Readings) != 1 || res.Readings[0].Value != 100 {
		t.Fatalf("probe %+v", res)
	}
}

func TestProbeRawRangeRTUAndTCP(t *testing.T) {
	for _, transport := range []string{"rtu", "tcp"} {
		t.Run(transport, func(t *testing.T) {
			req := probeReq()
			req.RequestID = "exact-read"
			req.Transport = transport
			req.RawRange = true
			req.Type = "u16"
			req.Count = 3
			fp := &fakePort{regs: map[int]uint16{10: 0x42c8, 11: 0, 12: 42}}
			if transport == "tcp" {
				req.Host, req.NetPort = fakeModbusTCP(t)
			}
			res := RunProbe(context.Background(), openerFor(fp, nil), req)
			if !res.OK || res.RequestID != req.RequestID || len(res.Readings) != 3 {
				t.Fatal(res)
			}
			for i, r := range res.Readings {
				want := float64([]int{0x42c8, 0, 42}[i])
				if transport == "tcp" {
					want = float64(110 + i)
				}
				if r.Value != want || r.PointID != fmt.Sprintf("r%d", 10+i) {
					t.Fatal(r, want)
				}
			}
			if transport == "rtu" && fp.sawFn != 3 {
				t.Fatal("non-read function", fp.sawFn)
			}
		})
	}
}

func TestProbeRangeRefusesUnsafeParametersBeforeOpening(t *testing.T) {
	for _, change := range []func(*ProbeRequest){func(p *ProbeRequest) { p.Func = 16 }, func(p *ProbeRequest) { p.Register = 65535; p.Count = 2 }, func(p *ProbeRequest) { p.Register = -1 }, func(p *ProbeRequest) { p.Type = "bool"; p.Func = 3 }, func(p *ProbeRequest) { p.Type = "f32"; p.Count = 1 }, func(p *ProbeRequest) { p.Transport = "http" }, func(p *ProbeRequest) { p.Transport = "tcp"; p.Host = "bad/path"; p.NetPort = 502 }} {
		req := probeReq()
		change(&req)
		opened := false
		res := RunProbe(context.Background(), func(string, *serial.Mode) (serial.Port, error) { opened = true; return nil, errTest }, req)
		if res.OK || opened {
			t.Fatal("unsafe probe opened transport", req, res)
		}
	}
}
