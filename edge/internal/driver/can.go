package driver

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

// canDriver decodes signals from a CAN bus (Linux SocketCAN, classic 8-byte frames) into numeric points.
// It only ever READS frames; there is no code path that sends one. It is not a DBC parser: each point
// carries its own signal definition in `key`:
//
//	0x123:start:length:le|be:u|s    id, start bit, bit length, byte order, unsigned or signed
//
// le is Intel order (start = least significant bit). be is Motorola order as DBC numbers it (start = most
// significant bit). IDs above 0x7FF are 29-bit extended. Scale and offset are not supported beyond
// `scale`. Tested against a simulated frame source and decode vectors I worked out by hand; NOT tested
// against a real vehicle or machine bus, real SocketCAN hardware, or a vcan interface in CI.
type canFrame struct {
	ID   uint32
	Ext  bool
	Data []byte
	At   time.Time
}

type canSignal struct {
	id        uint32
	ext       bool
	start     int
	length    int
	bigEndian bool
	signed    bool
}

var canKey = regexp.MustCompile(`^0x([0-9A-Fa-f]{1,8}):([0-9]{1,2}):([0-9]{1,2}):(le|be):([us])$`)

func parseCANKey(k string) (canSignal, error) {
	m := canKey.FindStringSubmatch(k)
	if m == nil {
		return canSignal{}, fmt.Errorf("key must look like 0x123:0:16:le:u")
	}
	id, err := strconv.ParseUint(m[1], 16, 32)
	if err != nil || id > 0x1FFFFFFF {
		return canSignal{}, fmt.Errorf("id out of range")
	}
	start, _ := strconv.Atoi(m[2])
	length, _ := strconv.Atoi(m[3])
	if length < 1 || length > 32 || start > 63 {
		return canSignal{}, fmt.Errorf("start must be 0-63 and length 1-32")
	}
	s := canSignal{id: uint32(id), ext: id > 0x7FF, start: start, length: length, bigEndian: m[4] == "be", signed: m[5] == "s"}
	if !s.bigEndian && start+length > 64 {
		return canSignal{}, fmt.Errorf("signal runs past the 8 data bytes")
	}
	return s, nil
}

// decode extracts the raw signal value from an 8-byte payload.
func (s canSignal) decode(data []byte) (float64, error) {
	if len(data) < 8 {
		pad := make([]byte, 8)
		copy(pad, data)
		data = pad
	}
	var raw uint64
	if !s.bigEndian {
		var all uint64
		for i := 7; i >= 0; i-- {
			all = all<<8 | uint64(data[i])
		}
		raw = (all >> uint(s.start)) & (1<<uint(s.length) - 1)
	} else {
		bit := s.start
		for i := 0; i < s.length; i++ {
			if bit < 0 || bit > 63 {
				return 0, fmt.Errorf("signal runs past the 8 data bytes")
			}
			b := (data[bit/8] >> uint(bit%8)) & 1
			raw = raw<<1 | uint64(b)
			if bit%8 == 0 { // next bit is the MSB of the following byte
				bit += 15
			} else {
				bit--
			}
		}
	}
	if s.signed && raw&(1<<uint(s.length-1)) != 0 {
		return float64(int64(raw) - int64(1)<<uint(s.length)), nil
	}
	return float64(raw), nil
}

type canSource interface {
	Frames(ctx context.Context) <-chan canFrame
	Close() error
}

type canDriver struct {
	dev     config.Device
	sigs    []canSignal
	src     canSource
	mu      sync.Mutex
	latest  map[uint64]canFrame
	cancel  context.CancelFunc
	maxAge  time.Duration
	started bool
}

func newCANWith(d config.Device, src canSource) (Driver, error) {
	if len(d.Points) == 0 {
		return nil, fmt.Errorf("device %s: no points", d.ID)
	}
	c := &canDriver{dev: d, src: src, latest: map[uint64]canFrame{}, maxAge: 30 * time.Second}
	if d.Interval > 0 && 3*d.Interval > c.maxAge {
		c.maxAge = 3 * d.Interval
	}
	for _, p := range d.Points {
		s, err := parseCANKey(p.Key)
		if err != nil {
			return nil, fmt.Errorf("device %s point %s: %w", d.ID, p.ID, err)
		}
		c.sigs = append(c.sigs, s)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	go func() {
		for f := range src.Frames(ctx) {
			k := uint64(f.ID)
			if f.Ext {
				k |= 1 << 32
			}
			c.mu.Lock()
			c.latest[k] = f
			c.mu.Unlock()
		}
	}()
	return c, nil
}

func (c *canDriver) Close() error {
	c.cancel()
	return c.src.Close()
}

func (c *canDriver) Poll(ctx context.Context) ([]Reading, error) {
	out := make([]Reading, 0, len(c.dev.Points))
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, p := range c.dev.Points {
		s := c.sigs[i]
		k := uint64(s.id)
		if s.ext {
			k |= 1 << 32
		}
		f, ok := c.latest[k]
		if !ok || time.Since(f.At) > c.maxAge {
			return out, fmt.Errorf("point %s: no recent frame with id 0x%X", p.ID, s.id)
		}
		raw, err := s.decode(f.Data)
		if err != nil {
			return out, fmt.Errorf("point %s: %w", p.ID, err)
		}
		scale := p.Scale
		if scale == 0 {
			scale = 1
		}
		v := raw * scale
		if math.IsNaN(v) || math.IsInf(v, 0) || v < p.Min || v > p.Max {
			return out, fmt.Errorf("point %s: value %v outside [%v,%v]", p.ID, v, p.Min, p.Max)
		}
		out = append(out, Reading{DeviceID: c.dev.ID, PointID: p.ID, Value: v, Unit: p.Unit})
	}
	return out, nil
}

// canIface validates the interface name taken from the config (port field).
var canIface = regexp.MustCompile(`^[a-z]{1,8}[0-9]{1,3}$`)

func newCAN(d config.Device) (Driver, error) {
	if !canIface.MatchString(strings.TrimPrefix(d.Port, "/dev/")) {
		return nil, fmt.Errorf("device %s: port must be a CAN interface name such as can0 or vcan0", d.ID)
	}
	src, err := openSocketCAN(d.Port)
	if err != nil {
		return nil, fmt.Errorf("device %s: %w", d.ID, err)
	}
	return newCANWith(d, src)
}
