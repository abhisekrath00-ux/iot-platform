package driver

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

// iec61850Driver is a read-only IEC 61850 MMS client (TPKT/COTP, ISO session,
// presentation and ACSE, then MMS Read). It reads named variables only; it
// never sends Write, Select or Operate, so it cannot control an IED.
//
// Config: host, net_port (default 102). Point `key` is `<domain>/<item>`
// using MMS naming, e.g. `IED1LD0/MMXU1$MX$TotW$mag$f`. Values may be float,
// integer, unsigned or boolean.
//
// STATUS: PARTIAL AND UNVERIFIED. The association bytes follow the
// well-known handshake used by open-source clients, but this driver has only
// been tested against a simulator written by the same author. It has never
// talked to a real IED. Expect to debug it against real hardware.
type iec61850Driver struct {
	dev    config.Device
	addr   string
	conn   net.Conn
	invoke int
}

func newIEC61850(d config.Device) (Driver, error) {
	if d.Host == "" {
		return nil, fmt.Errorf("device %s: host required for iec61850", d.ID)
	}
	if len(d.Points) == 0 {
		return nil, fmt.Errorf("device %s: no points", d.ID)
	}
	seen := map[string]bool{}
	for _, p := range d.Points {
		dom, item, ok := strings.Cut(p.Key, "/")
		if !ok || dom == "" || item == "" || seen[p.Key] || len(p.Key) > 200 {
			return nil, fmt.Errorf("device %s point %s: key must be a unique <domain>/<item>", d.ID, p.ID)
		}
		seen[p.Key] = true
	}
	port := d.NetPort
	if port == 0 {
		port = 102
	}
	return &iec61850Driver{dev: d, addr: net.JoinHostPort(d.Host, strconv.Itoa(port))}, nil
}

func (c *iec61850Driver) Close() error {
	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

// --- BER helpers ---

func berLen(n int) []byte {
	switch {
	case n < 128:
		return []byte{byte(n)}
	case n < 256:
		return []byte{0x81, byte(n)}
	}
	return []byte{0x82, byte(n >> 8), byte(n)}
}

func tlv(tag byte, parts ...[]byte) []byte {
	body := bytes.Join(parts, nil)
	return append(append([]byte{tag}, berLen(len(body))...), body...)
}

// berNext reads one TLV: tag, value, rest.
func berNext(b []byte) (tag byte, val, rest []byte, err error) {
	if len(b) < 2 {
		return 0, nil, nil, fmt.Errorf("truncated BER")
	}
	tag = b[0]
	n, p := int(b[1]), 2
	if n&0x80 != 0 {
		k := n & 0x7F
		if k == 0 || k > 3 || len(b) < 2+k {
			return 0, nil, nil, fmt.Errorf("bad BER length")
		}
		n = 0
		for i := 0; i < k; i++ {
			n = n<<8 | int(b[2+i])
		}
		p = 2 + k
	}
	if n < 0 || len(b) < p+n {
		return 0, nil, nil, fmt.Errorf("truncated BER value")
	}
	return tag, b[p : p+n], b[p+n:], nil
}

// berFind depth-first searches constructed TLVs for the first value with the tag.
func berFind(b []byte, want byte) ([]byte, bool) {
	for len(b) > 0 {
		tag, val, rest, err := berNext(b)
		if err != nil {
			return nil, false
		}
		if tag == want {
			return val, true
		}
		if tag&0x20 != 0 || tag == 0xC1 { // C1 = session user data, holds a presentation PPDU
			if v, ok := berFind(val, want); ok {
				return v, true
			}
		}
		b = rest
	}
	return nil, false
}

// spduBody skips an SPDU's identifier and length (short or 0xFF-prefixed long form).
func spduBody(d []byte) []byte {
	switch {
	case len(d) < 2:
		return nil
	case d[1] == 0xFF && len(d) >= 4:
		return d[4:]
	}
	return d[2:]
}

func tpkt(cotp []byte) []byte {
	return append([]byte{3, 0, byte((len(cotp) + 4) >> 8), byte(len(cotp) + 4)}, cotp...)
}

func readTPKT(r io.Reader) ([]byte, error) {
	h := make([]byte, 4)
	if _, err := io.ReadFull(r, h); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(h[2:]))
	if h[0] != 3 || n < 7 {
		return nil, fmt.Errorf("bad TPKT header")
	}
	b := make([]byte, n-4)
	_, err := io.ReadFull(r, b)
	return b, err
}

// cotpData strips a COTP DT header from a TPKT body.
func cotpData(b []byte) ([]byte, error) {
	if len(b) < 3 || b[1] != 0xF0 {
		return nil, fmt.Errorf("expected COTP data")
	}
	return b[int(b[0])+1:], nil
}

var mmsInitRequest = tlv(0xA8,
	[]byte{0x80, 3, 0x00, 0xFA, 0x00}, []byte{0x81, 1, 5}, []byte{0x82, 1, 5}, []byte{0x83, 1, 4},
	tlv(0xA4, []byte{0x80, 1, 1}, []byte{0x81, 3, 0x05, 0xF1, 0x00},
		[]byte{0x82, 0x0C, 0x03, 0xEE, 0x1C, 0x00, 0x00, 0x04, 0x08, 0x00, 0x00, 0x79, 0xEF, 0x18}))

func (c *iec61850Driver) send(cotpPayload []byte) error {
	_, err := c.conn.Write(tpkt(cotpPayload))
	return err
}

func (c *iec61850Driver) connect() error {
	conn, err := net.DialTimeout("tcp", c.addr, 3*time.Second)
	if err != nil {
		return err
	}
	c.conn = conn
	conn.SetDeadline(time.Now().Add(8 * time.Second))
	fail := func(e error) error { c.Close(); return e }
	cr := []byte{0x11, 0xE0, 0, 0, 0, 1, 0, 0xC0, 1, 0x0A, 0xC1, 2, 0, 1, 0xC2, 2, 0, 1}
	if err := c.send(cr); err != nil {
		return fail(err)
	}
	b, err := readTPKT(conn)
	if err != nil || len(b) < 2 || b[1] != 0xD0 {
		return fail(fmt.Errorf("COTP connection refused: %v", err))
	}
	if err := c.send(append([]byte{0x02, 0xF0, 0x80}, buildSessionConnect()...)); err != nil {
		return fail(err)
	}
	b, err = readTPKT(conn)
	if err != nil {
		return fail(err)
	}
	d, err := cotpData(b)
	if err != nil || len(d) < 2 || d[0] != 0x0E { // SPDU ACCEPT
		return fail(fmt.Errorf("association refused (no session accept)"))
	}
	if _, ok := berFind(spduBody(d), 0xAB); !ok { // MMS initiate-Response
		return fail(fmt.Errorf("association refused (no MMS initiate response)"))
	}
	return nil
}

func buildSessionConnect() []byte {
	aarq := tlv(0x60,
		tlv(0xA1, []byte{0x06, 5, 0x28, 0xCA, 0x22, 0x02, 0x03}),
		tlv(0xBE, tlv(0x28, []byte{0x06, 2, 0x51, 0x01}, []byte{0x02, 1, 3}, tlv(0xA0, mmsInitRequest))))
	cp := tlv(0x31,
		tlv(0xA0, []byte{0x80, 1, 1}),
		tlv(0xA2, []byte{0x81, 4, 0, 0, 0, 1}, []byte{0x82, 4, 0, 0, 0, 1}),
		tlv(0xA4,
			tlv(0x30, []byte{0x02, 1, 1}, []byte{0x06, 4, 0x52, 0x01, 0x00, 0x01}, tlv(0x30, []byte{0x06, 2, 0x51, 0x01})),
			tlv(0x30, []byte{0x02, 1, 3}, []byte{0x06, 5, 0x28, 0xCA, 0x22, 0x02, 0x01}, tlv(0x30, []byte{0x06, 2, 0x51, 0x01}))),
		tlv(0x61, tlv(0x30, []byte{0x02, 1, 1}, tlv(0xA0, aarq))))
	body := append([]byte{0x05, 6, 0x13, 1, 0, 0x16, 1, 2, 0x14, 2, 0, 2, 0x33, 2, 0, 1, 0x34, 2, 0, 1}, tlv(0xC1, cp)...)
	if len(body) >= 255 {
		return append([]byte{0x0D, 0xFF, byte(len(body) >> 8), byte(len(body))}, body...)
	}
	return append([]byte{0x0D, byte(len(body))}, body...)
}

// readRequest builds an MMS confirmed Read for the variables.
func readRequest(invoke int, keys []string) []byte {
	var vars [][]byte
	for _, k := range keys {
		dom, item, _ := strings.Cut(k, "/")
		vars = append(vars, tlv(0x30, tlv(0xA0, tlv(0xA1, tlv(0x1A, []byte(dom)), tlv(0x1A, []byte(item))))))
	}
	mms := tlv(0xA0, []byte{0x02, 1, byte(invoke)}, tlv(0xA4, tlv(0xA1, vars...)))
	ppdu := tlv(0x61, tlv(0x30, []byte{0x02, 1, 3}, tlv(0xA0, mms)))
	return append([]byte{0x02, 0xF0, 0x80, 0x01, 0x00, 0x01, 0x00}, ppdu...)
}

// decodeData maps one MMS Data element to a number.
func decodeData(tag byte, v []byte) (float64, error) {
	switch tag {
	case 0x83:
		if len(v) == 1 {
			return float64(v[0] & 1), nil
		}
	case 0x85, 0x86:
		if len(v) >= 1 && len(v) <= 8 {
			var u uint64
			for _, x := range v {
				u = u<<8 | uint64(x)
			}
			if tag == 0x85 {
				sh := uint(64 - 8*len(v))
				return float64(int64(u<<sh) >> sh), nil
			}
			return float64(u), nil
		}
	case 0x87:
		if len(v) == 5 && v[0] == 8 {
			return float64(math.Float32frombits(binary.BigEndian.Uint32(v[1:]))), nil
		}
		if len(v) == 9 && v[0] == 11 {
			return math.Float64frombits(binary.BigEndian.Uint64(v[1:])), nil
		}
	case 0x80:
		return 0, fmt.Errorf("data access error %d", int(v[len(v)-1]))
	}
	return 0, fmt.Errorf("unsupported MMS data type 0x%02x", tag)
}

func (c *iec61850Driver) Poll(ctx context.Context) ([]Reading, error) {
	if c.conn == nil {
		if err := c.connect(); err != nil {
			return nil, err
		}
	}
	fail := func(e error) ([]Reading, error) { c.Close(); return nil, e }
	keys := make([]string, len(c.dev.Points))
	for i, p := range c.dev.Points {
		keys[i] = p.Key
	}
	c.invoke = c.invoke%100 + 1
	c.conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := c.send(readRequest(c.invoke, keys)); err != nil {
		return fail(err)
	}
	b, err := readTPKT(c.conn)
	if err != nil {
		return fail(err)
	}
	d, err := cotpData(b)
	if err != nil || len(d) < 5 {
		return fail(fmt.Errorf("bad MMS response frame"))
	}
	d = d[4:]                   // session DATA SPDU header
	pdu, ok := berFind(d, 0xA1) // confirmed-ResponsePDU
	if !ok {
		return fail(fmt.Errorf("outstation did not return a confirmed response (reject or service error)"))
	}
	tag, inv, rest, err := berNext(pdu)
	if err != nil || tag != 0x02 || len(inv) != 1 || int(inv[0]) != c.invoke {
		return fail(fmt.Errorf("response invoke id mismatch"))
	}
	tag, readResp, _, err := berNext(rest)
	if err != nil || tag != 0xA4 {
		return fail(fmt.Errorf("not a read response"))
	}
	list, ok := berFind(readResp, 0xA1)
	if !ok {
		return fail(fmt.Errorf("read response has no results"))
	}
	var vals [][2]any
	for len(list) > 0 {
		tg, v, r, err := berNext(list)
		if err != nil {
			return fail(err)
		}
		vals = append(vals, [2]any{tg, v})
		list = r
	}
	if len(vals) != len(c.dev.Points) {
		return fail(fmt.Errorf("expected %d results, got %d", len(c.dev.Points), len(vals)))
	}
	out := make([]Reading, 0, len(vals))
	for i, p := range c.dev.Points {
		v, err := decodeData(vals[i][0].(byte), vals[i][1].([]byte))
		if err != nil {
			return out, fmt.Errorf("point %s: %v", p.ID, err)
		}
		scale := p.Scale
		if scale == 0 {
			scale = 1
		}
		v *= scale
		if math.IsNaN(v) || v < p.Min || v > p.Max {
			return out, fmt.Errorf("point %s: value %v outside [%v,%v]", p.ID, v, p.Min, p.Max)
		}
		out = append(out, Reading{DeviceID: c.dev.ID, PointID: p.ID, Value: v, Unit: p.Unit})
	}
	return out, nil
}
