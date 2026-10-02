// Package sparkplug decodes Eclipse Sparkplug B (spBv1.0) MQTT messages. It
// carries its own minimal protobuf wire reader, so there is no generated code
// and no new dependency, and it only reads: nothing here publishes (no NCMD /
// DCMD, no rebirth requests). Datasets, templates, properties and bytes
// metrics are skipped; only numeric and boolean metrics are returned.
package sparkplug

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
)

// Topic is spBv1.0/<group>/<type>/<edge_node>[/<device>].
type Topic struct {
	Group, Type, EdgeNode, Device string
}

// ParseTopic validates a Sparkplug B topic. The STATE topic and other
// namespaces return an error.
func ParseTopic(t string) (Topic, error) {
	p := strings.Split(t, "/")
	if len(p) < 4 || len(p) > 5 || p[0] != "spBv1.0" {
		return Topic{}, errors.New("not a spBv1.0 topic")
	}
	for _, s := range p[1:] {
		if s == "" || strings.ContainsAny(s, "+#") {
			return Topic{}, errors.New("empty or wildcard topic element")
		}
	}
	tp := Topic{Group: p[1], Type: p[2], EdgeNode: p[3]}
	if len(p) == 5 {
		tp.Device = p[4]
	}
	switch tp.Type {
	case "NBIRTH", "NDATA", "NDEATH", "NCMD":
		if tp.Device != "" {
			return Topic{}, errors.New("node message with device element")
		}
	case "DBIRTH", "DDATA", "DDEATH", "DCMD":
		if tp.Device == "" {
			return Topic{}, errors.New("device message without device element")
		}
	default:
		return Topic{}, fmt.Errorf("unknown message type %q", tp.Type)
	}
	return tp, nil
}

// Metric is one decoded numeric or boolean metric.
type Metric struct {
	Name         string
	Alias        uint64
	HasAlias     bool
	TimestampMs  uint64 // 0 when absent
	Value        float64
	IsNull       bool
	IsHistorical bool
	Datatype     uint32
	Numeric      bool // false: skipped type (string, dataset, ...), Value unset
}

type Payload struct {
	TimestampMs uint64
	Seq         uint64
	Metrics     []Metric
}

var errTrunc = errors.New("truncated protobuf")

func varint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(b) && i < 10; i++ {
		v |= uint64(b[i]&0x7F) << (7 * uint(i))
		if b[i] < 0x80 {
			return v, i + 1, nil
		}
	}
	return 0, 0, errTrunc
}

// fields iterates over protobuf fields; fn gets the field number, wire type,
// the varint/fixed value, and the bytes for length-delimited fields.
func fields(b []byte, fn func(num uint64, wt byte, v uint64, data []byte) error) error {
	for len(b) > 0 {
		key, n, err := varint(b)
		if err != nil {
			return err
		}
		b = b[n:]
		num, wt := key>>3, byte(key&7)
		var v uint64
		var data []byte
		switch wt {
		case 0:
			if v, n, err = varint(b); err != nil {
				return err
			}
			b = b[n:]
		case 1:
			if len(b) < 8 {
				return errTrunc
			}
			v = binary.LittleEndian.Uint64(b)
			b = b[8:]
		case 5:
			if len(b) < 4 {
				return errTrunc
			}
			v = uint64(binary.LittleEndian.Uint32(b))
			b = b[4:]
		case 2:
			l, n, err := varint(b)
			if err != nil || uint64(len(b)-n) < l {
				return errTrunc
			}
			data = b[n : n+int(l)]
			b = b[n+int(l):]
		default:
			return fmt.Errorf("unsupported wire type %d", wt)
		}
		if err := fn(num, wt, v, data); err != nil {
			return err
		}
	}
	return nil
}

// Decode parses a Sparkplug B Payload. It never panics on malformed input.
func Decode(b []byte) (*Payload, error) {
	if len(b) > 1<<20 {
		return nil, errors.New("payload too large")
	}
	p := &Payload{}
	err := fields(b, func(num uint64, wt byte, v uint64, data []byte) error {
		switch num {
		case 1:
			p.TimestampMs = v
		case 3:
			p.Seq = v
		case 2:
			if wt != 2 {
				return errors.New("metric must be length-delimited")
			}
			if len(p.Metrics) >= 5000 {
				return errors.New("too many metrics")
			}
			m, err := decodeMetric(data)
			if err != nil {
				return err
			}
			p.Metrics = append(p.Metrics, m)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

func decodeMetric(b []byte) (Metric, error) {
	var m Metric
	var raw uint64
	var wire byte
	var have bool
	err := fields(b, func(num uint64, wt byte, v uint64, data []byte) error {
		switch num {
		case 1:
			m.Name = string(data)
		case 2:
			m.Alias, m.HasAlias = v, true
		case 3:
			m.TimestampMs = v
		case 4:
			m.Datatype = uint32(v)
		case 5:
			m.IsHistorical = v != 0
		case 7:
			m.IsNull = v != 0
		case 10, 11, 12, 13, 14:
			raw, wire, have = v, byte(num), true
		}
		return nil
	})
	if err != nil {
		return m, err
	}
	if !have || m.IsNull {
		return m, nil
	}
	switch m.Datatype {
	case 1: // Int8
		m.Value, m.Numeric = float64(int8(raw)), wire == 10
	case 2:
		m.Value, m.Numeric = float64(int16(raw)), wire == 10
	case 3:
		m.Value, m.Numeric = float64(int32(raw)), wire == 10
	case 4: // Int64
		m.Value, m.Numeric = float64(int64(raw)), wire == 11
	case 5, 6, 7: // UInt8/16/32
		m.Value, m.Numeric = float64(uint32(raw)), wire == 10
	case 8:
		m.Value, m.Numeric = float64(raw), wire == 11
	case 9:
		m.Value, m.Numeric = float64(math.Float32frombits(uint32(raw))), wire == 12
	case 10:
		m.Value, m.Numeric = math.Float64frombits(raw), wire == 13
	case 11:
		m.Numeric = wire == 14
		if raw != 0 {
			m.Value = 1
		}
	}
	if m.Numeric && (math.IsNaN(m.Value) || math.IsInf(m.Value, 0)) {
		m.Numeric = false
	}
	return m, nil
}

// Aliases remembers the name<->alias map announced in BIRTH messages so DATA
// messages that carry only an alias can be resolved. Keyed by edge node + device.
type Aliases struct {
	mu sync.Mutex
	m  map[string]map[uint64]string
}

func key(t Topic) string { return t.Group + "/" + t.EdgeNode + "/" + t.Device }

// Resolve fills Name from the alias table (BIRTH messages update the table) and
// returns only metrics that have a name. DEATH messages clear the table.
func (a *Aliases) Resolve(t Topic, p *Payload) []Metric {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.m == nil {
		a.m = map[string]map[uint64]string{}
	}
	k := key(t)
	switch t.Type {
	case "NDEATH", "DDEATH":
		delete(a.m, k)
		return nil
	case "NBIRTH", "DBIRTH":
		a.m[k] = map[uint64]string{} // a birth replaces the table
		for _, m := range p.Metrics {
			if m.HasAlias && m.Name != "" {
				if len(a.m[k]) < 10000 {
					a.m[k][m.Alias] = m.Name
				}
			}
		}
	}
	out := make([]Metric, 0, len(p.Metrics))
	for _, m := range p.Metrics {
		if m.Name == "" && m.HasAlias {
			m.Name = a.m[k][m.Alias]
		}
		if m.Name != "" {
			out = append(out, m)
		}
	}
	return out
}
