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

// iec104 is a read-only IEC 60870-5-104 controlling station (client) for
// substations, RTUs and protection relays. Each poll sends a general
// interrogation and collects the monitored values it returns. There is no
// command path (no C_SC/C_SE), so it can never operate equipment.
//
// Config: host, net_port (default 2404), address = ASDU common address (CA).
// Points carry `ioa` (information object address). Supported monitor types:
// M_SP_NA_1 (1), M_DP_NA_1 (3), M_ME_NA_1 (9, normalized), M_ME_NB_1 (11,
// scaled), M_ME_NC_1 (13, float) and their CP56Time2a variants (30, 31, 34, 35, 36).
type iec104Driver struct {
	dev  config.Device
	addr string
	conn net.Conn
	vs   uint16 // send sequence N(S)
	vr   uint16 // receive sequence N(R)
}

func newIEC104(d config.Device) (Driver, error) {
	if d.Host == "" {
		return nil, fmt.Errorf("device %s: host required for iec104", d.ID)
	}
	if d.Address < 1 || d.Address > 65534 {
		return nil, fmt.Errorf("device %s: address (ASDU common address) must be 1-65534", d.ID)
	}
	seen := map[int]bool{}
	for _, p := range d.Points {
		if p.IOA < 1 || p.IOA > 0xFFFFFF || seen[p.IOA] {
			return nil, fmt.Errorf("device %s point %s: ioa must be unique, 1-16777215", d.ID, p.ID)
		}
		seen[p.IOA] = true
	}
	if len(d.Points) == 0 {
		return nil, fmt.Errorf("device %s: no points", d.ID)
	}
	port := d.NetPort
	if port == 0 {
		port = 2404
	}
	return &iec104Driver{dev: d, addr: net.JoinHostPort(d.Host, strconv.Itoa(port))}, nil
}

func (c *iec104Driver) Close() error {
	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

func (c *iec104Driver) drop() { c.Close() }

func (c *iec104Driver) sendU(ctrl byte) error {
	_, err := c.conn.Write([]byte{0x68, 4, ctrl, 0, 0, 0})
	return err
}

func (c *iec104Driver) sendS() error {
	b := []byte{0x68, 4, 0x01, 0, 0, 0}
	binary.LittleEndian.PutUint16(b[4:], c.vr<<1)
	_, err := c.conn.Write(b)
	return err
}

// readAPDU reads one APDU: returns control field and ASDU bytes.
func (c *iec104Driver) readAPDU() (ctrl [4]byte, asdu []byte, err error) {
	hdr := make([]byte, 2)
	if _, err = io.ReadFull(c.conn, hdr); err != nil {
		return
	}
	if hdr[0] != 0x68 || hdr[1] < 4 {
		err = fmt.Errorf("bad APDU start/length %x", hdr)
		return
	}
	body := make([]byte, hdr[1])
	if _, err = io.ReadFull(c.conn, body); err != nil {
		return
	}
	copy(ctrl[:], body[:4])
	return ctrl, body[4:], nil
}

func (c *iec104Driver) connect() error {
	conn, err := net.DialTimeout("tcp", c.addr, 3*time.Second)
	if err != nil {
		return err
	}
	c.conn, c.vs, c.vr = conn, 0, 0
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := c.sendU(0x07); err != nil { // STARTDT act
		c.drop()
		return err
	}
	for {
		ctrl, _, err := c.readAPDU()
		if err != nil {
			c.drop()
			return fmt.Errorf("STARTDT: %w", err)
		}
		if ctrl[0] == 0x0B { // STARTDT con
			return nil
		}
	}
}

// parseASDU extracts (ioa -> value) from one monitor-direction ASDU and also
// reports the cause of transmission and type id.
func parseASDU(a []byte) (typ byte, cot byte, ca int, vals map[int]float64, err error) {
	if len(a) < 6 {
		return 0, 0, 0, nil, fmt.Errorf("short ASDU")
	}
	typ = a[0]
	n, seq := int(a[1]&0x7F), a[1]&0x80 != 0
	cot = a[2] & 0x7F // low 6 bits cause, bit 6 = negative confirm
	ca = int(binary.LittleEndian.Uint16(a[4:6]))
	var size, tag int // bytes of information element and time tag
	switch typ {
	case 1, 3:
		size = 1
	case 9, 11:
		size = 3
	case 13:
		size = 5
	case 30, 31:
		size, tag = 1, 7
	case 34, 35:
		size, tag = 3, 7
	case 36:
		size, tag = 5, 7
	default:
		return typ, cot, ca, nil, nil // not a value we map (e.g. activation confirmation)
	}
	vals = map[int]float64{}
	p := 6
	var base int
	for i := 0; i < n; i++ {
		var ioa int
		if !seq || i == 0 {
			if p+3 > len(a) {
				return typ, cot, ca, vals, fmt.Errorf("truncated ASDU")
			}
			ioa = int(a[p]) | int(a[p+1])<<8 | int(a[p+2])<<16
			p += 3
			base = ioa
		} else {
			ioa = base + i
		}
		if p+size+tag > len(a) {
			return typ, cot, ca, vals, fmt.Errorf("truncated ASDU")
		}
		e := a[p : p+size]
		p += size + tag
		var v float64
		quality := byte(0)
		switch typ {
		case 1, 30:
			v, quality = float64(e[0]&1), e[0]&0xF0
		case 3, 31:
			v, quality = float64(e[0]&3), e[0]&0xF0 // 1 off, 2 on, 0/3 intermediate/indeterminate
		case 9, 34:
			v, quality = float64(int16(binary.LittleEndian.Uint16(e)))/32768, e[2]
		case 11, 35:
			v, quality = float64(int16(binary.LittleEndian.Uint16(e))), e[2]
		case 13, 36:
			v, quality = float64(math.Float32frombits(binary.LittleEndian.Uint32(e))), e[4]
		}
		// IV (bit 7) invalid or NT (bit 6) not topical: never trust the value.
		// OV (bit 0, measured values only): overflow.
		if quality&0xC0 != 0 || (typ != 1 && typ != 3 && typ != 30 && typ != 31 && quality&0x01 != 0) {
			v = math.NaN()
		}
		vals[ioa] = v
	}
	return typ, cot, ca, vals, nil
}

func (c *iec104Driver) Poll(ctx context.Context) ([]Reading, error) {
	if c.conn == nil {
		if err := c.connect(); err != nil {
			return nil, err
		}
	}
	// General interrogation: C_IC_NA_1, COT=6 (activation), QOI=20 (station).
	asdu := []byte{100, 1, 6, 0, byte(c.dev.Address), byte(c.dev.Address >> 8), 0, 0, 0, 20}
	frame := []byte{0x68, byte(4 + len(asdu)), 0, 0, 0, 0}
	binary.LittleEndian.PutUint16(frame[2:], c.vs<<1)
	binary.LittleEndian.PutUint16(frame[4:], c.vr<<1)
	frame = append(frame, asdu...)
	c.conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.conn.Write(frame); err != nil {
		c.drop()
		return nil, err
	}
	c.vs++
	got := map[int]float64{}
	for done := false; !done; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ctrl, a, err := c.readAPDU()
		if err != nil {
			c.drop()
			return nil, err
		}
		switch {
		case ctrl[0]&1 == 0: // I-format
			c.vr++
			typ, cot, ca, vals, err := parseASDU(a)
			if err != nil {
				c.drop()
				return nil, err
			}
			if ca != c.dev.Address {
				continue // another station's data on a shared link
			}
			if typ == 100 && cot == 10 { // activation termination: interrogation complete
				done = true
			}
			if typ == 100 && cot&0x40 != 0 { // negative confirmation
				c.drop()
				return nil, fmt.Errorf("interrogation rejected by outstation")
			}
			for k, v := range vals {
				got[k] = v
			}
			if c.vr%8 == 0 {
				c.sendS()
			}
		case ctrl[0]&3 == 3 && ctrl[0] == 0x43: // TESTFR act
			c.sendU(0x83)
		}
	}
	c.sendS()
	out := make([]Reading, 0, len(c.dev.Points))
	for _, p := range c.dev.Points {
		v, ok := got[p.IOA]
		if !ok {
			return out, fmt.Errorf("point %s: no value for IOA %d", p.ID, p.IOA)
		}
		if math.IsNaN(v) {
			return out, fmt.Errorf("point %s: outstation flagged value invalid or not topical", p.ID)
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
