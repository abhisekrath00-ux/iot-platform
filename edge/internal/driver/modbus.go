package driver

import (
	"context"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"go.bug.st/serial"
)

// modbusMeter implements a minimal Modbus RTU reader (function 0x04, input
// registers). Confirm the register map against the meter datasheet before
// enabling a profile in the field.
type modbusMeter struct {
	port serial.Port
	dev  config.Device
}

func newModbusMeter(port serial.Port, dev config.Device) *modbusMeter {
	return &modbusMeter{port: port, dev: dev}
}

func crc16(b []byte) uint16 {
	var crc uint16 = 0xFFFF
	for _, v := range b {
		crc ^= uint16(v)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

func (m *modbusMeter) readInputRegister(ctx context.Context, reg int) (uint16, error) {
	req := []byte{byte(m.dev.Address), 0x04, byte(reg >> 8), byte(reg), 0x00, 0x01}
	c := crc16(req)
	req = append(req, byte(c), byte(c>>8))
	m.port.ResetInputBuffer()
	if _, err := m.port.Write(req); err != nil {
		return 0, err
	}
	m.port.SetReadTimeout(500 * time.Millisecond)
	buf := make([]byte, 7)
	n, err := m.port.Read(buf)
	if err != nil {
		return 0, err
	}
	if n < 7 {
		return 0, fmt.Errorf("short frame: %d bytes", n)
	}
	if crc16(buf[:n-2]) != binary.LittleEndian.Uint16(buf[n-2:]) {
		return 0, fmt.Errorf("crc mismatch")
	}
	if buf[0] != byte(m.dev.Address) || buf[1] != 0x04 {
		return 0, fmt.Errorf("unexpected response header %x %x", buf[0], buf[1])
	}
	return binary.BigEndian.Uint16(buf[3:5]), nil
}

func (m *modbusMeter) Poll(ctx context.Context) ([]Reading, error) {
	out := make([]Reading, 0, len(m.dev.Points))
	for _, p := range m.dev.Points {
		raw, err := m.readInputRegister(ctx, p.Register)
		if err != nil {
			return out, fmt.Errorf("point %s: %w", p.ID, err)
		}
		scale := p.Scale
		if scale == 0 {
			scale = 1
		}
		v := float64(raw) * scale
		if v < p.Min || v > p.Max {
			return out, fmt.Errorf("point %s: value %v outside [%v,%v]", p.ID, v, p.Min, p.Max)
		}
		out = append(out, Reading{DeviceID: m.dev.ID, PointID: p.ID, Value: v, Unit: p.Unit})
	}
	return out, nil
}

func (m *modbusMeter) Close() error { return m.port.Close() }
