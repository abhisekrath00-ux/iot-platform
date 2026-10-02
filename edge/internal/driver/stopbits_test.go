package driver

import (
	"testing"

	"go.bug.st/serial"
)

// Regression: serial.StopBits(1) is 1.5 stop bits in go.bug.st/serial.
func TestStopBitsMode(t *testing.T) {
	for n, want := range map[int]serial.StopBits{0: serial.OneStopBit, 1: serial.OneStopBit, 2: serial.TwoStopBits} {
		if got := stopBitsMode(n); got != want {
			t.Errorf("stopBitsMode(%d) = %v, want %v", n, got, want)
		}
	}
	if serial.StopBits(1) == serial.OneStopBit {
		t.Log("library enum changed; revisit stopBitsMode")
	}
}
