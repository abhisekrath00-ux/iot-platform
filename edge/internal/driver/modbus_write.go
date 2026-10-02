package driver

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

// Writer is implemented by drivers that can write to an allowlisted point.
// It is only ever reached through the edge command gate (approved, four-eyes,
// unexpired, allowlisted action); drivers never write on their own.
type Writer interface {
	Write(ctx context.Context, pointID string, value float64) error
}

// encodeWords is the inverse of decodeWords: it lays a 32-bit value out in the
// device's word order.
func encodeWords(raw uint32, order string) ([]uint16, error) {
	a, b, c, d := uint16(raw>>24)&0xFF, uint16(raw>>16)&0xFF, uint16(raw>>8)&0xFF, uint16(raw)&0xFF
	switch order {
	case "", "abcd":
		return []uint16{a<<8 | b, c<<8 | d}, nil
	case "badc":
		return []uint16{b<<8 | a, d<<8 | c}, nil
	case "cdab":
		return []uint16{c<<8 | d, a<<8 | b}, nil
	case "dcba":
		return []uint16{d<<8 | c, b<<8 | a}, nil
	}
	return nil, fmt.Errorf("unknown word_order %q", order)
}

// Write sends one validated value to a point listed under the device's
// `writes`. The value is range-checked against that entry's min/max before any
// frame is built; a write point without a finite range is refused.
func (g *modbusGeneric) Write(ctx context.Context, pointID string, value float64) error {
	if g.write == nil {
		return fmt.Errorf("device %s does not support writes", g.dev.ID)
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("non-finite value")
	}
	for _, p := range g.dev.Writes {
		if p.ID != pointID {
			continue
		}
		if !(p.Max > p.Min) {
			return fmt.Errorf("write point %s has no valid min/max range", p.ID)
		}
		if value < p.Min || value > p.Max {
			return fmt.Errorf("value %v outside allowed [%v,%v]", value, p.Min, p.Max)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		scale := p.Scale
		if scale == 0 {
			scale = 1
		}
		raw := value / scale
		g.mu.Lock()
		defer g.mu.Unlock()
		switch p.Func {
		case 5:
			if value != 0 && value != 1 {
				return fmt.Errorf("coil value must be 0 or 1")
			}
			body := []byte{0, 0}
			if value == 1 {
				body = []byte{0xFF, 0x00}
			}
			return g.write(5, p.Register, body)
		case 6, 16:
			typ := p.Type
			if typ == "" {
				typ = "u16"
			}
			n, err := regCount(typ)
			if err != nil {
				return err
			}
			var words []uint16
			switch typ {
			case "u16":
				if raw < 0 || raw > math.MaxUint16 {
					return fmt.Errorf("scaled value out of u16 range")
				}
				words = []uint16{uint16(math.Round(raw))}
			case "i16":
				if raw < math.MinInt16 || raw > math.MaxInt16 {
					return fmt.Errorf("scaled value out of i16 range")
				}
				words = []uint16{uint16(int16(math.Round(raw)))}
			case "u32", "i32", "f32":
				var u uint32
				switch typ {
				case "f32":
					u = math.Float32bits(float32(raw))
				case "u32":
					if raw < 0 || raw > math.MaxUint32 {
						return fmt.Errorf("scaled value out of u32 range")
					}
					u = uint32(math.Round(raw))
				default:
					if raw < math.MinInt32 || raw > math.MaxInt32 {
						return fmt.Errorf("scaled value out of i32 range")
					}
					u = uint32(int32(math.Round(raw)))
				}
				if words, err = encodeWords(u, p.WordOrder); err != nil {
					return err
				}
			}
			if p.Func == 6 && n != 1 {
				return fmt.Errorf("function 6 writes one register; use func 16 for %s", typ)
			}
			if p.Func == 6 {
				return g.write(6, p.Register, binary.BigEndian.AppendUint16(nil, words[0]))
			}
			body := binary.BigEndian.AppendUint16(nil, uint16(len(words)))
			body = append(body, byte(2*len(words)))
			for _, w := range words {
				body = binary.BigEndian.AppendUint16(body, w)
			}
			return g.write(16, p.Register, body)
		default:
			return fmt.Errorf("write point %s: func must be 5, 6 or 16", p.ID)
		}
	}
	return fmt.Errorf("point %q is not in the device's write allowlist", pointID)
}

// writeRTU frames one write over the serial port and verifies the echo.
func (g *modbusGeneric) writeRTU(fn, reg int, body []byte) error {
	pdu := append([]byte{byte(fn), byte(reg >> 8), byte(reg)}, body...)
	req := append([]byte{byte(g.dev.Address)}, pdu...)
	c := crc16(req)
	req = append(req, byte(c), byte(c>>8))
	g.port.ResetInputBuffer()
	if _, err := g.port.Write(req); err != nil {
		return err
	}
	g.port.SetReadTimeout(500 * time.Millisecond)
	buf := make([]byte, 8)
	n, err := g.port.Read(buf)
	if err != nil {
		return err
	}
	if n < 5 {
		return fmt.Errorf("short frame: %d bytes", n)
	}
	if crc16(buf[:n-2]) != binary.LittleEndian.Uint16(buf[n-2:]) {
		return fmt.Errorf("crc mismatch")
	}
	if buf[0] != byte(g.dev.Address) {
		return fmt.Errorf("response from unexpected address %d", buf[0])
	}
	return checkWriteEcho(fn, pdu, buf[1:n-2])
}

// checkWriteEcho verifies a write response: an exception is an error, and the
// device must echo the register address (and value for fn 5/6, quantity for 16).
func checkWriteEcho(fn int, reqPDU, resp []byte) error {
	if len(resp) >= 2 && resp[0] == byte(fn)|0x80 {
		return fmt.Errorf("modbus exception code %d", resp[1])
	}
	want := reqPDU[:5] // fn, reg, then value (fn 5/6) or quantity (fn 16)
	if len(resp) != 5 || string(resp) != string(want) {
		return fmt.Errorf("write not confirmed: device echo does not match request")
	}
	return nil
}
