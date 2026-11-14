package driver

import (
	"fmt"
	"sync"

	"go.bug.st/serial"
)

// Production serial opens all pass through this reservation, including polls,
// probes, discovery and temporary explorer connections. The platform lease is
// acquired before the serial library can change framing/line state.
var serialOwners = struct {
	sync.Mutex
	held map[string]bool
}{held: map[string]bool{}}

type ownedSerialPort struct {
	serial.Port
	once     sync.Once
	closeErr error
	release  func()
}

func (p *ownedSerialPort) Close() error {
	p.once.Do(func() { p.closeErr = p.Port.Close(); p.release() })
	return p.closeErr
}

// OpenOwnedSerial refuses a second owner instead of changing another driver's
// baud/parity or stealing its reads. Unix uses a canonical character-device
// identity and a nonblocking advisory device lock shared by cooperating edge
// processes. Windows additionally has mandatory exclusive CreateFile access.
// Third-party Unix programs that ignore advisory locks, an already-open older
// agent, and privileged programs are outside that cooperative guarantee.
func OpenOwnedSerial(name string, mode *serial.Mode) (serial.Port, error) {
	if mode == nil || mode.BaudRate < 1 || mode.BaudRate > 4000000 || mode.DataBits < 5 || mode.DataBits > 8 || mode.Parity < serial.NoParity || mode.Parity > serial.SpaceParity || mode.StopBits < serial.OneStopBit || mode.StopBits > serial.TwoStopBits {
		return nil, fmt.Errorf("invalid serial framing")
	}
	key, canonical, err := serialIdentity(name)
	if err != nil {
		return nil, err
	}
	serialOwners.Lock()
	if serialOwners.held[key] {
		serialOwners.Unlock()
		return nil, fmt.Errorf("serial port is already owned by this agent; select its existing device connection")
	}
	serialOwners.held[key] = true
	serialOwners.Unlock()
	releaseLocal := func() { serialOwners.Lock(); delete(serialOwners.held, key); serialOwners.Unlock() }
	unlock, err := serialLease(canonical, key)
	if err != nil {
		releaseLocal()
		return nil, fmt.Errorf("serial port ownership unavailable: %w", err)
	}
	p, err := serial.Open(canonical, mode)
	if err != nil {
		unlock()
		releaseLocal()
		return nil, err
	}
	return &ownedSerialPort{Port: p, release: func() { unlock(); releaseLocal() }}, nil
}
