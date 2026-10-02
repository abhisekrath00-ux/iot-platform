package driver

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

// bacnetDriver reads present-value from BACnet/IP objects (building automation:
// HVAC controllers, meters, chillers, VAV boxes) with unicast ReadProperty over
// UDP (default 47808). Read-only: no WriteProperty, no COV, no discovery/Who-Is,
// no BBMD or routed devices. Point `key` is "<type>:<instance>", e.g. "ai:12".
type bacnetDriver struct {
	dev    config.Device
	addr   *net.UDPAddr
	conn   *net.UDPConn
	invoke byte
	objs   []uint32
}

var bacnetTypes = map[string]uint32{"ai": 0, "ao": 1, "av": 2, "bi": 3, "bo": 4, "bv": 5, "mi": 13, "mo": 14, "mv": 19}

func parseBACnetKey(k string) (uint32, error) {
	t, inst, ok := strings.Cut(strings.ToLower(strings.TrimSpace(k)), ":")
	ot, known := bacnetTypes[t]
	n, err := strconv.ParseUint(inst, 10, 32)
	if !ok || !known || err != nil || n > 0x3FFFFF {
		return 0, fmt.Errorf("key %q must be <ai|ao|av|bi|bo|bv|mi|mo|mv>:<instance 0-4194302>", k)
	}
	return ot<<22 | uint32(n), nil
}

func newBACnet(d config.Device) (Driver, error) {
	if d.Host == "" {
		return nil, fmt.Errorf("device %s: host required for bacnet", d.ID)
	}
	if len(d.Points) == 0 {
		return nil, fmt.Errorf("device %s: no points", d.ID)
	}
	port := d.NetPort
	if port == 0 {
		port = 47808
	}
	a, err := net.ResolveUDPAddr("udp", net.JoinHostPort(d.Host, strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("device %s: %w", d.ID, err)
	}
	b := &bacnetDriver{dev: d, addr: a}
	for _, p := range d.Points {
		o, err := parseBACnetKey(p.Key)
		if err != nil {
			return nil, fmt.Errorf("device %s point %s: %w", d.ID, p.ID, err)
		}
		b.objs = append(b.objs, o)
	}
	return b, nil
}

func (b *bacnetDriver) Close() error {
	if b.conn != nil {
		err := b.conn.Close()
		b.conn = nil
		return err
	}
	return nil
}

func (b *bacnetDriver) readPresentValue(obj uint32) (float64, error) {
	if b.conn == nil {
		c, err := net.DialUDP("udp", nil, b.addr)
		if err != nil {
			return 0, err
		}
		b.conn = c
	}
	b.invoke++
	id := b.invoke
	apdu := []byte{0x00, 0x05, id, 0x0C, 0x0C, byte(obj >> 24), byte(obj >> 16), byte(obj >> 8), byte(obj), 0x19, 85}
	npdu := []byte{0x01, 0x04}
	msg := []byte{0x81, 0x0A, 0, 0}
	msg = append(append(msg, npdu...), apdu...)
	binary.BigEndian.PutUint16(msg[2:], uint16(len(msg)))
	b.conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := b.conn.Write(msg); err != nil {
		return 0, err
	}
	buf := make([]byte, 1500)
	for {
		n, err := b.conn.Read(buf)
		if err != nil {
			return 0, err
		}
		v, matched, err := parseBACnetReply(buf[:n], id, obj)
		if !matched {
			continue // stray datagram or a reply to an earlier timed-out request
		}
		return v, err
	}
}

// parseBACnetReply decodes a ReadProperty ComplexACK. matched is false when the
// datagram is not the reply to this invoke id.
func parseBACnetReply(p []byte, invoke byte, obj uint32) (v float64, matched bool, err error) {
	if len(p) < 7 || p[0] != 0x81 {
		return 0, false, nil
	}
	i := 4
	if p[i] != 1 {
		return 0, false, nil
	}
	ctrl := p[i+1]
	i += 2
	if ctrl&0x20 != 0 { // destination specifier
		if i+3 > len(p) {
			return 0, false, nil
		}
		i += 3 + int(p[i+2])
	}
	if ctrl&0x08 != 0 { // source specifier
		if i+3 > len(p) {
			return 0, false, nil
		}
		i += 3 + int(p[i+2])
	}
	if ctrl&0x20 != 0 {
		i++ // hop count
	}
	if ctrl&0x80 != 0 || i >= len(p) { // network-layer message
		return 0, false, nil
	}
	typ := p[i] >> 4
	switch typ {
	case 3: // ComplexACK
		if p[i]&0x0C != 0 { // segmented / more follows: not supported
			return 0, p[i+1] == invoke, fmt.Errorf("segmented reply not supported")
		}
		if i+3 > len(p) || p[i+1] != invoke {
			return 0, false, nil
		}
		if p[i+2] != 0x0C {
			return 0, true, fmt.Errorf("unexpected service %d", p[i+2])
		}
		a := p[i+3:]
		if len(a) < 8 || a[0] != 0x0C || binary.BigEndian.Uint32(a[1:5]) != obj || a[5] != 0x19 || a[6] != 85 || a[7] != 0x3E {
			return 0, true, fmt.Errorf("reply does not match request")
		}
		v, err = bacnetValue(a[8:])
		return v, true, err
	case 5, 6, 7: // Error, Reject, Abort
		if i+1 < len(p) && p[i+1] == invoke {
			return 0, true, fmt.Errorf("device refused the read (BACnet %s)", map[byte]string{5: "error", 6: "reject", 7: "abort"}[typ])
		}
	}
	return 0, false, nil
}

func bacnetValue(a []byte) (float64, error) {
	if len(a) < 1 {
		return 0, fmt.Errorf("empty value")
	}
	tag, class := a[0]>>4, a[0]&0x08
	lvt := int(a[0] & 0x07)
	if class != 0 {
		return 0, fmt.Errorf("context tag where application value expected")
	}
	body := a[1:]
	if tag == 1 { // boolean: value is in the length bits
		return float64(lvt & 1), nil
	}
	if lvt == 5 {
		return 0, fmt.Errorf("extended length value not supported")
	}
	if len(body) < lvt {
		return 0, fmt.Errorf("truncated value")
	}
	body = body[:lvt]
	switch tag {
	case 2, 9: // unsigned, enumerated
		var n uint64
		for _, c := range body {
			n = n<<8 | uint64(c)
		}
		return float64(n), nil
	case 3: // signed
		var n int64
		if len(body) > 0 && body[0]&0x80 != 0 {
			n = -1
		}
		for _, c := range body {
			n = n<<8 | int64(c)
		}
		return float64(n), nil
	case 4:
		if lvt != 4 {
			return 0, fmt.Errorf("bad real length")
		}
		return float64(math.Float32frombits(binary.BigEndian.Uint32(body))), nil
	case 5:
		if lvt != 8 {
			return 0, fmt.Errorf("bad double length")
		}
		return math.Float64frombits(binary.BigEndian.Uint64(body)), nil
	}
	return 0, fmt.Errorf("non-numeric BACnet type %d", tag)
}

func (b *bacnetDriver) Poll(ctx context.Context) ([]Reading, error) {
	out := make([]Reading, 0, len(b.dev.Points))
	for i, p := range b.dev.Points {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		v, err := b.readPresentValue(b.objs[i])
		if err != nil {
			return out, fmt.Errorf("point %s: %w", p.ID, err)
		}
		scale := p.Scale
		if scale == 0 {
			scale = 1
		}
		v *= scale
		if math.IsNaN(v) || v < p.Min || v > p.Max {
			return out, fmt.Errorf("point %s: value %v outside [%v,%v]", p.ID, v, p.Min, p.Max)
		}
		out = append(out, Reading{DeviceID: b.dev.ID, PointID: p.ID, Value: v, Unit: p.Unit})
	}
	return out, nil
}
