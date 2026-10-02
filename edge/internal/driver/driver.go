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

// PortOpener opens a serial port; injectable for tests and one-off probes.
type PortOpener func(name string, mode *serial.Mode) (serial.Port, error)

// New returns the driver for the device profile.
func New(d config.Device) (Driver, error) {
	return NewWithOpener(serial.Open, d)
}

// NewWithOpener is New with the port opener injected.
func NewWithOpener(open PortOpener, d config.Device) (Driver, error) {
	// Network profiles need no serial port.
	switch d.Profile {
	case "modbus-tcp":
		return newModbusTCP(d)
	case "opcua":
		return newOPCUA(d)
	case "snmp":
		return newSNMP(d)
	case "bacnet":
		return newBACnet(d)
	case "dnp3":
		return newDNP3(d)
	case "coap":
		return newCoAP(d)
	case "iec61850":
		return newIEC61850(d)
	case "iec104":
		return newIEC104(d)
	}
	mode := &serial.Mode{BaudRate: d.Baud, DataBits: d.DataBits, StopBits: stopBitsMode(d.StopBits)}
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
	port, err := open(d.Port, mode)
	if err != nil {
		return nil, fmt.Errorf("device %s: open %s: %w", d.ID, d.Port, err)
	}
	switch d.Profile {
	case "modbus-generic":
		// Fully config-driven: any Modbus RTU sensor model, no code change.
		return newModbusGeneric(port, d), nil
	case "modbus-energy-meter":
		return newModbusMeter(port, d), nil
	case "door-contact":
		return newDoorContact(port, d), nil
	case "serial-json":
		// STM32 / Arduino / ESP32 over UART or USB-CDC: newline-delimited
		// JSON, key=value or CSV lines, mapped to points via config.
		return newSerialLine(port, d), nil
	default:
		port.Close()
		return nil, fmt.Errorf("device %s: unknown profile %q", d.ID, d.Profile)
	}
}

// stopBitsMode maps a stop-bit count (1 or 2; 0 means the default of 1) to the
// serial library's enum. The enum is NOT the count: serial.StopBits(1) is 1.5
// stop bits, which most adapters reject or misread, so a plain conversion of
// "stop_bits: 1" would break every real serial link.
func stopBitsMode(n int) serial.StopBits {
	if n == 2 {
		return serial.TwoStopBits
	}
	return serial.OneStopBit
}
