package driver

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

// coapDriver is a read-only CoAP (RFC 7252) client over UDP: it sends a
// confirmable GET per resource path and reads a numeric value from the
// response. It never sends PUT/POST/DELETE.
//
// Config: host, net_port (default 5683). Point `key` is the resource path,
// optionally followed by `#field` to read a (dotted) field from a JSON
// payload, e.g. `/sensors/env#temp` or `/temp` (plain-text number body).
// Several points on one path share one GET per poll.
type coapDriver struct {
	dev  config.Device
	addr string
	mid  uint16
}

func newCoAP(d config.Device) (Driver, error) {
	if d.Host == "" {
		return nil, fmt.Errorf("device %s: host required for coap", d.ID)
	}
	if len(d.Points) == 0 {
		return nil, fmt.Errorf("device %s: no points", d.ID)
	}
	for _, p := range d.Points {
		path, _, _ := strings.Cut(p.Key, "#")
		if !strings.HasPrefix(path, "/") || len(path) < 2 {
			return nil, fmt.Errorf("device %s point %s: key must start with a resource path like /temp", d.ID, p.ID)
		}
	}
	port := d.NetPort
	if port == 0 {
		port = 5683
	}
	return &coapDriver{dev: d, addr: net.JoinHostPort(d.Host, strconv.Itoa(port)), mid: uint16(time.Now().UnixNano())}, nil
}

func (c *coapDriver) Close() error { return nil }

func coapOpt(out []byte, prev, num int, val []byte) []byte {
	d, l := num-prev, len(val)
	ext := func(n int) (nib byte, e []byte) {
		switch {
		case n < 13:
			return byte(n), nil
		case n < 269:
			return 13, []byte{byte(n - 13)}
		}
		return 14, binary.BigEndian.AppendUint16(nil, uint16(n-269))
	}
	dn, de := ext(d)
	ln, le := ext(l)
	out = append(out, dn<<4|ln)
	out = append(out, de...)
	out = append(out, le...)
	return append(out, val...)
}

// coapGet builds a confirmable GET for the path.
func coapGet(mid uint16, token []byte, path string) []byte {
	b := []byte{0x40 | byte(len(token)), 0x01, byte(mid >> 8), byte(mid)}
	b = append(b, token...)
	prev := 0
	for _, seg := range strings.Split(strings.Trim(path, "/"), "/") {
		b = coapOpt(b, prev, 11, []byte(seg))
		prev = 11
	}
	return b
}

// parseCoAP returns the response code (class*100+detail), token, message id, type and payload.
func parseCoAP(b []byte) (code int, typ byte, mid uint16, token, payload []byte, err error) {
	if len(b) < 4 || b[0]>>6 != 1 {
		return 0, 0, 0, nil, nil, fmt.Errorf("not a CoAP v1 message")
	}
	tkl := int(b[0] & 0x0F)
	if tkl > 8 || len(b) < 4+tkl {
		return 0, 0, 0, nil, nil, fmt.Errorf("bad CoAP token length")
	}
	typ, code = b[0]>>4&3, int(b[1]>>5)*100+int(b[1]&0x1F)
	mid, token = binary.BigEndian.Uint16(b[2:]), b[4:4+tkl]
	p := 4 + tkl
	for p < len(b) && b[p] != 0xFF { // skip options
		dn, ln := int(b[p]>>4), int(b[p]&0x0F)
		p++
		for _, n := range []*int{&dn, &ln} {
			switch *n {
			case 13:
				if p >= len(b) {
					return 0, 0, 0, nil, nil, fmt.Errorf("truncated option")
				}
				*n = int(b[p]) + 13
				p++
			case 14:
				if p+2 > len(b) {
					return 0, 0, 0, nil, nil, fmt.Errorf("truncated option")
				}
				*n = int(binary.BigEndian.Uint16(b[p:])) + 269
				p += 2
			case 15:
				return 0, 0, 0, nil, nil, fmt.Errorf("bad option nibble")
			}
		}
		if p+ln > len(b) {
			return 0, 0, 0, nil, nil, fmt.Errorf("truncated option")
		}
		p += ln
	}
	if p < len(b) {
		payload = b[p+1:]
	}
	return
}

// exchange sends a CON GET and waits for the matching response, retransmitting
// with doubling backoff (RFC 7252 section 4.2), up to 3 retries.
func (c *coapDriver) exchange(ctx context.Context, path string) ([]byte, error) {
	conn, err := net.Dial("udp", c.addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	c.mid++
	token := binary.BigEndian.AppendUint32(nil, uint32(time.Now().UnixNano())^uint32(c.mid))
	req := coapGet(c.mid, token, path)
	wait := 500 * time.Millisecond
	buf := make([]byte, 2048)
	for try := 0; try < 4; try++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, err := conn.Write(req); err != nil {
			return nil, err
		}
		conn.SetReadDeadline(time.Now().Add(wait))
		for {
			n, err := conn.Read(buf)
			if err != nil {
				break // timeout: retransmit
			}
			code, typ, mid, tok, payload, perr := parseCoAP(buf[:n])
			if perr != nil || typ == 3 { // junk or RST
				continue
			}
			if typ == 2 && mid == c.mid && code == 0 { // empty ACK: separate response follows
				wait = 5 * time.Second
				continue
			}
			if string(tok) != string(token) {
				continue
			}
			if typ == 0 { // separate CON response: acknowledge
				conn.Write([]byte{0x60, 0, byte(mid >> 8), byte(mid)})
			}
			if code != 205 { // 2.05 Content
				return nil, fmt.Errorf("coap %s: response %d.%02d", path, code/100, code%100)
			}
			return payload, nil
		}
		wait *= 2
	}
	return nil, fmt.Errorf("coap %s: no response", path)
}

func jsonPath(v any, path string) (float64, bool) {
	for _, k := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return 0, false
		}
		if v, ok = m[k]; !ok {
			return 0, false
		}
	}
	switch x := v.(type) {
	case float64:
		return x, true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

func (c *coapDriver) Poll(ctx context.Context) ([]Reading, error) {
	bodies := map[string][]byte{}
	out := make([]Reading, 0, len(c.dev.Points))
	for _, p := range c.dev.Points {
		path, field, _ := strings.Cut(p.Key, "#")
		body, ok := bodies[path]
		if !ok {
			var err error
			if body, err = c.exchange(ctx, path); err != nil {
				return out, err
			}
			bodies[path] = body
		}
		var v float64
		if field == "" {
			f, err := strconv.ParseFloat(strings.TrimSpace(string(body)), 64)
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
				return out, fmt.Errorf("point %s: body is not a number", p.ID)
			}
			v = f
		} else {
			var doc any
			if err := json.Unmarshal(body, &doc); err != nil {
				return out, fmt.Errorf("point %s: body is not JSON", p.ID)
			}
			f, ok := jsonPath(doc, field)
			if !ok {
				return out, fmt.Errorf("point %s: no numeric field %q", p.ID, field)
			}
			v = f
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
