package driver

import (
	"bytes"
	"context"
	"testing"

	"go.bug.st/serial"
)

// onlyAddr is a bus where just one slave answers; every other address times out.
type onlyAddr struct {
	*fakePort
	addr byte
}

func (o *onlyAddr) Write(p []byte) (int, error) {
	n, err := o.fakePort.Write(p)
	if p[0] != o.addr {
		o.fakePort.readBuf = bytes.NewReader(nil)
	}
	return n, err
}

func TestScanFindsOnlyResponders(t *testing.T) {
	open := func(string, *serial.Mode) (serial.Port, error) {
		return &onlyAddr{fakePort: &fakePort{regs: map[int]uint16{0: 230}}, addr: 5}, nil
	}
	found, err := ScanModbusRTU(context.Background(), open, ScanRequest{Port: "x", Baud: 9600, DataBits: 8, StopBits: 1, Parity: "none", From: 1, To: 8, Func: 4})
	if err != nil || len(found) != 1 || found[0].Address != 5 || found[0].Value != 230 {
		t.Fatalf("%v %v", found, err)
	}
}

func TestScanRejectsBadRangesAndWrites(t *testing.T) {
	for _, r := range []ScanRequest{{From: 0, To: 5}, {From: 1, To: 248}, {From: 9, To: 3}, {From: 1, To: 3, Func: 6}, {From: 1, To: 3, Func: 16}} {
		if _, err := ScanModbusRTU(context.Background(), nil, r); err == nil {
			t.Errorf("accepted %+v", r)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	open := func(string, *serial.Mode) (serial.Port, error) { return &fakePort{regs: map[int]uint16{}}, nil }
	if f, err := ScanModbusRTU(ctx, open, ScanRequest{From: 1, To: 5}); err == nil || len(f) != 0 {
		t.Errorf("cancelled scan: %v %v", f, err)
	}
}
