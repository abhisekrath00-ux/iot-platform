// Package driver reads points from field devices. One implementation per
// device profile (e.g. Modbus RTU energy meter, door contact). Drivers are
// sandboxed to their serial port and never talk to the network.
package driver

import (
	"context"
	"fmt"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"go.bug.st/serial"
)

// Reading is one validated point sample.
type Reading struct {
	DeviceID string
	PointID  string
	Value    float64
	Unit     string
}

// Driver polls one configured device.
type Driver interface {
	Poll(ctx context.Context) ([]Reading, error)
	Close() error
}

// New returns the driver for the device profile.
func New(d config.Device) (Driver, error) {
	mode := &serial.Mode{BaudRate: d.Baud, DataBits: d.DataBits, StopBits: serial.StopBits(d.StopBits)}
	switch d.Parity {
	case "none", "":
		mode.Parity = serial.NoParity
	case "odd":
		mode.Parity = serial.OddParity
	case "even":
		mode.Parity = serial.EvenParity
	default:
		return nil, fmt.Errorf("device %s: unknown parity %q", d.ID, d.Parity)
	}
	port, err := serial.Open(d.Port, mode)
	if err != nil {
		return nil, fmt.Errorf("device %s: open %s: %w", d.ID, d.Port, err)
	}
	switch d.Profile {
	case "modbus-energy-meter":
		return newModbusMeter(port, d), nil
	case "door-contact":
		return newDoorContact(port, d), nil
	default:
		port.Close()
		return nil, fmt.Errorf("device %s: unknown profile %q", d.ID, d.Profile)
	}
}
