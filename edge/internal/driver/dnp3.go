package driver

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"strconv"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

// dnp3Driver is a read-only DNP3 (IEEE 1815) master over TCP. Each poll sends
// one class 0 integrity READ (group 60 var 1) and maps the static points it
// returns. Only the READ function is ever sent (plus link/app confirmations),
// so it cannot operate equipment: there is no SELECT/OPERATE/DIRECT_OPERATE.
//
// Config: host, net_port (default 20000), address = outstation link address.
// The master link address is 1. Points: `register` = point index, `key` =
// point class: ai (analog input g30), bi (binary input g1), ctr (counter g20),
// bo (binary output status g10). Points flagged not ONLINE are rejected.
type dnp3Driver struct {
	dev  config.Device
	addr string
	conn net.Conn
	seq  byte
}

func newDNP3(d config.Device) (Driver, error) {
	if d.Host == "" {
		return nil, fmt.Errorf("device %s: host required for dnp3", d.ID)
	}
	if d.Address < 0 || d.Address > 65519 {
		return nil, fmt.Errorf("device %s: address (outstation link address) must be 0-65519", d.ID)
	}
	if len(d.Points) == 0 {
		return nil, fmt.Errorf("device %s: no points", d.ID)
	}
	seen := map[string]bool{}
	for _, p := range d.Points {
		switch p.Key {
		case "ai", "bi", "ctr", "bo":
		default:
			return nil, fmt.Errorf("device %s point %s: key must be ai, bi, ctr or bo", d.ID, p.ID)
		}
		k := p.Key + ":" + strconv.Itoa(p.Register)
		if p.Register < 0 || p.Register > 65535 || seen[k] {
			return nil, fmt.Errorf("device %s point %s: register (index) must be unique per key, 0-65535", d.ID, p.ID)
		}
		seen[k] = true
	}
	port := d.NetPort
	if port == 0 {
		port = 20000
	}
	return &dnp3Driver{dev: d, addr: net.JoinHostPort(d.Host, strconv.Itoa(port))}, nil
}

func (c *dnp3Driver) Close() error {
	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

// dnpCRC is the DNP3 CRC-16 (poly 0x3D65, reflected, complemented).
func dnpCRC(b []byte) uint16 {
	var crc uint16
	for _, x := range b {
		crc ^= uint16(x)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0xA6BC
			} else {
				crc >>= 1
			}
		}
	}
	return ^crc
}

// dnpFrame builds a link frame (unconfirmed user data from the master) around one transport segment.
func dnpFrame(dest, src uint16, ctrl byte, user []byte) []byte {
	h := []byte{0x05, 0x64, byte(5 + len(user)), ctrl, 0, 0, 0, 0}
	binary.LittleEndian.PutUint16(h[4:], dest)
	binary.LittleEndian.PutUint16(h[6:], src)
	out := append(h, 0, 0)
	binary.LittleEndian.PutUint16(out[8:], dnpCRC(h))
	for len(user) > 0 {
		n := len(user)
		if n > 16 {
			n = 16
		}
		blk := user[:n]
		out = append(out, blk...)
		out = binary.LittleEndian.AppendUint16(out, dnpCRC(blk))
		user = user[n:]
	}
	return out
}

// readDNPFrame reads one link frame and returns control byte, source address and user data (CRCs verified).
func readDNPFrame(r io.Reader) (ctrl byte, src uint16, user []byte, err error) {
	h := make([]byte, 10)
	if _, err = io.ReadFull(r, h); err != nil {
		return
	}
	if h[0] != 0x05 || h[1] != 0x64 || h[2] < 5 {
		err = fmt.Errorf("bad DNP3 link header")
		return
	}
	if binary.LittleEndian.Uint16(h[8:]) != dnpCRC(h[:8]) {
		err = fmt.Errorf("DNP3 link header CRC mismatch")
		return
	}
	ctrl, src = h[3], binary.LittleEndian.Uint16(h[6:])
	left := int(h[2]) - 5
	for left > 0 {
		n := left
		if n > 16 {
			n = 16
		}
		blk := make([]byte, n+2)
		if _, err = io.ReadFull(r, blk); err != nil {
			return
		}
		if binary.LittleEndian.Uint16(blk[n:]) != dnpCRC(blk[:n]) {
			err = fmt.Errorf("DNP3 data block CRC mismatch")
			return
		}
		user = append(user, blk[:n]...)
		left -= n
	}
	return
}

// readFragment reassembles transport segments into one application fragment.
func (c *dnp3Driver) readFragment() ([]byte, error) {
	var frag []byte
	started := false
	for {
		_, src, user, err := readDNPFrame(c.conn)
		if err != nil {
			return nil, err
		}
		if src != uint16(c.dev.Address) || len(user) == 0 {
			continue // other station, or link-only frame
		}
		fir, fin := user[0]&0x40 != 0, user[0]&0x80 != 0
		if fir {
			frag, started = nil, true
		}
		if !started {
			continue
		}
		frag = append(frag, user[1:]...)
		if fin {
			return frag, nil
		}
	}
}

// dnp3Size returns the per-object size (excluding index) for the variations we map, 0 if unsupported.
func dnp3Size(g, v byte) int {
	switch {
	case g == 1 && v == 2, g == 10 && v == 2:
		return 1
	case g == 30 && v == 1, g == 30 && v == 5, g == 20 && v == 1:
		return 5
	case g == 30 && v == 2, g == 20 && v == 2:
		return 3
	case g == 30 && v == 3, g == 20 && v == 5:
		return 4
	case g == 30 && v == 4, g == 20 && v == 6:
		return 2
	case g == 30 && v == 6:
		return 9
	}
	return 0
}

func dnp3Value(g, v byte, b []byte) float64 {
	var flag byte
	var raw []byte
	hasFlag := true
	switch {
	case g == 30 && v == 3, g == 20 && v == 5, g == 30 && v == 4, g == 20 && v == 6:
		hasFlag = false
	}
	if hasFlag {
		flag, raw = b[0], b[1:]
	} else {
		raw = b
	}
	if hasFlag && flag&0x01 == 0 { // not ONLINE
		return math.NaN()
	}
	if hasFlag && g != 1 && g != 10 && flag&0x20 != 0 { // OVER_RANGE
		return math.NaN()
	}
	switch {
	case g == 1, g == 10:
		return float64(flag >> 7 & 1)
	case g == 30 && v == 1, g == 30 && v == 3:
		return float64(int32(binary.LittleEndian.Uint32(raw)))
	case g == 30 && v == 2, g == 30 && v == 4:
		return float64(int16(binary.LittleEndian.Uint16(raw)))
	case g == 30 && v == 5:
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(raw)))
	case g == 30 && v == 6:
		return math.Float64frombits(binary.LittleEndian.Uint64(raw))
	case g == 20 && (v == 1 || v == 5):
		return float64(binary.LittleEndian.Uint32(raw))
	default: // g20 v2/v6
		return float64(binary.LittleEndian.Uint16(raw))
	}
}

func dnp3Class(g byte) string {
	switch g {
	case 1:
		return "bi"
	case 10:
		return "bo"
	case 20:
		return "ctr"
	case 30:
		return "ai"
	}
	return ""
}

// parseDNP3Objects extracts class:index -> value from a response fragment (after app header + IIN).
func parseDNP3Objects(b []byte, got map[string]float64) error {
	for len(b) > 0 {
		if len(b) < 3 {
			return fmt.Errorf("truncated object header")
		}
		g, v, q := b[0], b[1], b[2]
		b = b[3:]
		size := dnp3Size(g, v)
		cls := dnp3Class(g)
		var start, count, idxLen int
		switch q {
		case 0x00:
			if len(b) < 2 {
				return fmt.Errorf("truncated range")
			}
			start, count, b = int(b[0]), int(b[1])-int(b[0])+1, b[2:]
		case 0x01:
			if len(b) < 4 {
				return fmt.Errorf("truncated range")
			}
			s, e := int(binary.LittleEndian.Uint16(b)), int(binary.LittleEndian.Uint16(b[2:]))
			start, count, b = s, e-s+1, b[4:]
		case 0x07:
			if len(b) < 1 {
				return fmt.Errorf("truncated count")
			}
			count, b = int(b[0]), b[1:]
		case 0x17:
			if len(b) < 1 {
				return fmt.Errorf("truncated count")
			}
			count, idxLen, b = int(b[0]), 1, b[1:]
		case 0x28:
			if len(b) < 2 {
				return fmt.Errorf("truncated count")
			}
			count, idxLen, b = int(binary.LittleEndian.Uint16(b)), 2, b[2:]
		default:
			return fmt.Errorf("unsupported qualifier 0x%02x for g%dv%d", q, g, v)
		}
		if size == 0 || cls == "" {
			if g == 60 || g == 80 { // class markers / IIN carry no data we map
				continue
			}
			return fmt.Errorf("unsupported object g%dv%d", g, v)
		}
		if count < 0 {
			return fmt.Errorf("bad range")
		}
		for i := 0; i < count; i++ {
			idx := start + i
			if idxLen > 0 {
				if len(b) < idxLen+size {
					return fmt.Errorf("truncated object data")
				}
				idx = int(b[0])
				if idxLen == 2 {
					idx = int(binary.LittleEndian.Uint16(b))
				}
				b = b[idxLen:]
			}
			if len(b) < size {
				return fmt.Errorf("truncated object data")
			}
			got[cls+":"+strconv.Itoa(idx)] = dnp3Value(g, v, b[:size])
			b = b[size:]
		}
	}
	return nil
}

func (c *dnp3Driver) Poll(ctx context.Context) ([]Reading, error) {
	if c.conn == nil {
		conn, err := net.DialTimeout("tcp", c.addr, 3*time.Second)
		if err != nil {
			return nil, err
		}
		c.conn = conn
	}
	fail := func(err error) ([]Reading, error) { c.Close(); return nil, err }
	c.seq = (c.seq + 1) & 0x0F
	// transport FIR|FIN, app FIR|FIN, READ class 0
	req := []byte{0xC0, 0xC0 | c.seq, 0x01, 60, 1, 0x06}
	c.conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.conn.Write(dnpFrame(uint16(c.dev.Address), 1, 0xC4, req)); err != nil {
		return fail(err)
	}
	got := map[string]float64{}
	for {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		frag, err := c.readFragment()
		if err != nil {
			return fail(err)
		}
		if len(frag) < 4 || frag[1] != 0x81 {
			return fail(fmt.Errorf("unexpected application response"))
		}
		iin := binary.LittleEndian.Uint16(frag[2:4])
		if iin&0x0F00 != 0 { // IIN2: no func support, object unknown, parameter error, overflow
			return fail(fmt.Errorf("outstation rejected class 0 read (IIN 0x%04x)", iin))
		}
		if err := parseDNP3Objects(frag[4:], got); err != nil {
			return fail(err)
		}
		if frag[0]&0x20 != 0 { // CON: confirm
			c.conn.Write(dnpFrame(uint16(c.dev.Address), 1, 0xC4, []byte{0xC0, 0xC0 | frag[0]&0x0F, 0x00}))
		}
		if frag[0]&0x80 != 0 { // FIN
			break
		}
	}
	out := make([]Reading, 0, len(c.dev.Points))
	for _, p := range c.dev.Points {
		v, ok := got[p.Key+":"+strconv.Itoa(p.Register)]
		if !ok {
			return out, fmt.Errorf("point %s: no value for %s index %d", p.ID, p.Key, p.Register)
		}
		if math.IsNaN(v) {
			return out, fmt.Errorf("point %s: outstation flagged value offline or over-range", p.ID)
		}
		scale := p.Scale
		if scale == 0 {
			scale = 1
		}
		v *= scale
		if v < p.Min || v > p.Max {
			return out, fmt.Errorf("point %s: value %v outside [%v,%v]", p.ID, v, p.Min, p.Max)
		}
		out = append(out, Reading{DeviceID: c.dev.ID, PointID: p.ID, Value: v, Unit: p.Unit})
	}
	return out, nil
}
