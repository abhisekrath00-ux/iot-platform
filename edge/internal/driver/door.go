package driver

import (
	"context"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"go.bug.st/serial"
)

// doorContact reads a simple contact sensor presented over serial as an ASCII
// line ("0\n" closed, "1\n" open) from a small interface MCU, with debounce.
// Adjust to the actual sensor interface from its datasheet.
type doorContact struct {
	port     serial.Port
	dev      config.Device
	last     float64
	lastFlip time.Time
}

func newDoorContact(port serial.Port, dev config.Device) *doorContact {
	return &doorContact{port: port, dev: dev, last: -1}
}

func (d *doorContact) Poll(ctx context.Context) ([]Reading, error) {
	d.port.SetReadTimeout(200 * time.Millisecond)
	buf := make([]byte, 1)
	if _, err := d.port.Read(buf); err != nil {
		return nil, err
	}
	var v float64
	switch buf[0] {
	case '0':
		v = 0
	case '1':
		v = 1
	default:
		return nil, nil // noise; skip cycle
	}
	// debounce: report a flip only after 100ms stable
	if v != d.last {
		if time.Since(d.lastFlip) < 100*time.Millisecond {
			v = d.last
		} else {
			d.last, d.lastFlip = v, time.Now()
		}
	}
	if d.last < 0 {
		return nil, nil
	}
	out := make([]Reading, 0, len(d.dev.Points))
	for _, p := range d.dev.Points {
		out = append(out, Reading{DeviceID: d.dev.ID, PointID: p.ID, Value: v, Unit: p.Unit})
	}
	return out, nil
}

func (d *doorContact) Close() error { return d.port.Close() }
