package driver

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"go.bug.st/serial"
)

// modbusGeneric is a fully config-driven Modbus RTU driver: each point names
// its function code, data type, word order and scale, so new sensor models
// are onboarded from the dashboard/profile config without a code change.
type modbusGeneric struct {
	port serial.Port // nil for Modbus TCP
	dev  config.Device
	// read performs one Modbus read; RTU-over-serial or TCP framing.
	read func(fn, reg, count int) ([]byte, error)
	// write performs one Modbus write (fn 5, 6 or 16); body is the bytes after
	// the register address. It returns nil only when the device echoed the request.
	write func(fn, reg int, body []byte) error
	close func() error
	mu    sync.Mutex // one bus transaction sequence at a time (poll vs write)
}

func newModbusGeneric(port serial.Port, dev config.Device) *modbusGeneric {
	g := &modbusGeneric{port: port, dev: dev}
	g.read = g.readRegisters
	g.write = g.writeRTU
	g.close = port.Close
	return g
}

func pointFunc(p config.Point) int {
	if p.Func != 0 {
		return p.Func
	}
	return 4 // legacy points were all input registers
}

func pointType(p config.Point) string {
	if p.Type != "" {
		return p.Type
	}
	if pointFunc(p) == 1 || pointFunc(p) == 2 {
		return "bool"
	}
	return "u16"
}

// regCount is the number of 16-bit registers a type occupies.
func regCount(typ string) (int, error) {
	switch typ {
	case "u16", "i16":
		return 1, nil
	case "u32", "i32", "f32":
		return 2, nil
	}
	return 0, fmt.Errorf("unknown type %q", typ)
}

// decodeWords converts raw registers to a value honoring word order.
func decodeWords(words []uint16, typ, order string) (float64, error) {
	if order == "" {
		order = "abcd"
	}
	// Normalize to big-endian byte sequence.
	b := make([]byte, 0, len(words)*2)
	switch order {
	case "abcd": // word1,word2 as sent
		for _, w := range words {
			b = append(b, byte(w>>8), byte(w))
		}
	case "badc": // byte-swapped within each word
		for _, w := range words {
			b = append(b, byte(w), byte(w>>8))
		}
	case "cdab": // word order swapped (little-endian words)
		for i := len(words) - 1; i >= 0; i-- {
			b = append(b, byte(words[i]>>8), byte(words[i]))
		}
	case "dcba": // both swaps
		for i := len(words) - 1; i >= 0; i-- {
			b = append(b, byte(words[i]), byte(words[i]>>8))
		}
	default:
		return 0, fmt.Errorf("unknown word_order %q", order)
	}
	switch typ {
	case "u16":
		return float64(binary.BigEndian.Uint16(b)), nil
	case "i16":
		return float64(int16(binary.BigEndian.Uint16(b))), nil
	case "u32":
		return float64(binary.BigEndian.Uint32(b)), nil
	case "i32":
		return float64(int32(binary.BigEndian.Uint32(b))), nil
	case "f32":
		return float64(math.Float32frombits(binary.BigEndian.Uint32(b))), nil
	}
	return 0, fmt.Errorf("unknown type %q", typ)
}

// readRegisters issues one Modbus request and returns the data words (fn 3/4)
// or packed status bytes (fn 1/2).
func (g *modbusGeneric) readRegisters(fn, reg, count int) ([]byte, error) {
	req := []byte{byte(g.dev.Address), byte(fn), byte(reg >> 8), byte(reg), byte(count >> 8), byte(count)}
	c := crc16(req)
	req = append(req, byte(c), byte(c>>8))
	g.port.ResetInputBuffer()
	if _, err := g.port.Write(req); err != nil {
		return nil, err
	}
	g.port.SetReadTimeout(500 * time.Millisecond)
	// Response: addr fn bytecount data... crc(2). Read generously, then parse.
	buf := make([]byte, 5+2*count+2)
	n, err := g.port.Read(buf)
	if err != nil {
		return nil, err
	}
	if n < 5 {
		return nil, fmt.Errorf("short frame: %d bytes", n)
	}
	if crc16(buf[:n-2]) != binary.LittleEndian.Uint16(buf[n-2:]) {
		return nil, fmt.Errorf("crc mismatch")
	}
	if buf[0] != byte(g.dev.Address) || buf[1] != byte(fn) {
		return nil, fmt.Errorf("unexpected response header %x %x", buf[0], buf[1])
	}
	byteCount := int(buf[2])
	if n < 3+byteCount+2 {
		return nil, fmt.Errorf("frame shorter than declared byte count")
	}
	return buf[3 : 3+byteCount], nil
}

func (g *modbusGeneric) Poll(ctx context.Context) ([]Reading, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.read == nil {
		g.read = g.readRegisters
	}
	out := make([]Reading, 0, len(g.dev.Points))
	for _, p := range g.dev.Points {
		fn, typ := pointFunc(p), pointType(p)
		var v float64
		if fn == 1 || fn == 2 {
			data, err := g.read(fn, p.Register, 1)
			if err != nil {
				return out, fmt.Errorf("point %s: %w", p.ID, err)
			}
			if len(data) > 0 && data[0]&1 == 1 {
				v = 1
			}
		} else {
			n, err := regCount(typ)
			if err != nil {
				return out, fmt.Errorf("point %s: %w", p.ID, err)
			}
			data, err := g.read(fn, p.Register, n)
			if err != nil {
				return out, fmt.Errorf("point %s: %w", p.ID, err)
			}
			if len(data) < n*2 {
				return out, fmt.Errorf("point %s: short data", p.ID)
			}
			words := make([]uint16, n)
			for i := range words {
				words[i] = binary.BigEndian.Uint16(data[i*2:])
			}
			v, err = decodeWords(words, typ, p.WordOrder)
			if err != nil {
				return out, fmt.Errorf("point %s: %w", p.ID, err)
			}
		}
		scale := p.Scale
		if scale == 0 {
			scale = 1
		}
		v *= scale
		if v < p.Min || v > p.Max {
			return out, fmt.Errorf("point %s: value %v outside [%v,%v]", p.ID, v, p.Min, p.Max)
		}
		out = append(out, Reading{DeviceID: g.dev.ID, PointID: p.ID, Value: v, Unit: p.Unit})
	}
	return out, nil
}

func (g *modbusGeneric) Close() error {
	if g.close == nil {
		return g.port.Close()
	}
	return g.close()
}
