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
	write  func(fn, reg int, body []byte) error
	close  func() error
	mu     sync.Mutex // one bus transaction sequence at a time (poll vs write)
	closed bool       // protected by mu; stale explorer connections cannot reopen after close
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
	case "u32", "i32", "f32", "t5", "t6":
		return 2, nil
	case "f64":
		return 4, nil
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
	case "t5", "t6":
		// Iskra decade-exponent measurement (vendor "Data types decoding", MC 3x0/MC 7x0
		// user manuals): byte 0 is a signed 8-bit decade exponent, bytes 1-3 are the
		// 24-bit mantissa (unsigned for T5, signed for T6); value = mantissa x 10^exp.
		// The exponent is part of every reading, so no static scale can represent it.
		exp := int8(b[0])
		mant := int32(b[1])<<16 | int32(b[2])<<8 | int32(b[3])
		if typ == "t6" && mant&0x800000 != 0 {
			mant -= 1 << 24
		}
		return float64(mant) * math.Pow10(int(exp)), nil
	case "f64":
		return math.Float64frombits(binary.BigEndian.Uint64(b)), nil
	}
	return 0, fmt.Errorf("unknown type %q", typ)
}

// readRegisters issues one Modbus request and returns the data words (fn 3/4)
// or packed status bytes (fn 1/2).
// readRTUExact assembles one response across OS read fragments. A zero-byte
// serial timeout is an error, not io.ReadFull's unbounded zero-progress loop.
// Only the response is continued; the request is never sent again.
func readRTUExact(port serial.Port, dst []byte, deadline time.Time) error {
	for len(dst) > 0 {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("RTU response deadline exceeded")
		}
		if remaining > 500*time.Millisecond {
			remaining = 500 * time.Millisecond
		}
		if err := port.SetReadTimeout(remaining); err != nil {
			return err
		}
		n, err := port.Read(dst)
		if n < 0 || n > len(dst) {
			return fmt.Errorf("invalid serial read length")
		}
		dst = dst[n:]
		if len(dst) == 0 {
			return nil
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("RTU response timed out without progress")
		}
	}
	return nil
}

func (g *modbusGeneric) readRegisters(fn, reg, count int) ([]byte, error) {
	if fn < 1 || fn > 4 || reg < 0 || reg > 65535 || count < 1 || count > 125 || reg+count > 65536 || g.dev.Address < 1 || g.dev.Address > 247 {
		return nil, fmt.Errorf("invalid read-only RTU request")
	}
	req := []byte{byte(g.dev.Address), byte(fn), byte(reg >> 8), byte(reg), byte(count >> 8), byte(count)}
	c := crc16(req)
	req = append(req, byte(c), byte(c>>8))
	if err := g.port.ResetInputBuffer(); err != nil {
		return nil, err
	}
	n, err := g.port.Write(req)
	if err != nil {
		return nil, err
	}
	if n != len(req) {
		return nil, fmt.Errorf("short RTU request write; no retry sent")
	}
	deadline := time.Now().Add(2 * time.Second)
	hdr := make([]byte, 3)
	if err := readRTUExact(g.port, hdr, deadline); err != nil {
		return nil, err
	}
	if hdr[0] != byte(g.dev.Address) || (hdr[1] != byte(fn) && hdr[1] != byte(fn)|0x80) {
		return nil, fmt.Errorf("unexpected RTU response address/function")
	}
	size := count * 2
	if fn <= 2 {
		size = (count + 7) / 8
	}
	exception := hdr[1] == byte(fn)|0x80
	if exception {
		size = 0
	} else if int(hdr[2]) != size {
		return nil, fmt.Errorf("unexpected RTU response byte count")
	}
	tail := make([]byte, size+2)
	if err := readRTUExact(g.port, tail, deadline); err != nil {
		return nil, err
	}
	frame := append(hdr, tail...)
	if crc16(frame[:len(frame)-2]) != binary.LittleEndian.Uint16(frame[len(frame)-2:]) {
		return nil, fmt.Errorf("crc mismatch")
	}
	if exception {
		return nil, fmt.Errorf("modbus exception code %d", hdr[2])
	}
	return tail[:size], nil
}

func (g *modbusGeneric) Poll(ctx context.Context) ([]Reading, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil, fmt.Errorf("device connection closed")
	}
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
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil
	}
	g.closed = true
	if g.close == nil {
		return g.port.Close()
	}
	return g.close()
}
