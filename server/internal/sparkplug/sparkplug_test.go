package sparkplug

import (
	"encoding/binary"
	"math"
	"testing"
)

// minimal protobuf encoder for tests
func vi(v uint64) []byte {
	var b []byte
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}
func fv(num int, v uint64) []byte { return append(vi(uint64(num<<3)), vi(v)...) }
func fb(num int, d []byte) []byte {
	return append(append(vi(uint64(num<<3|2)), vi(uint64(len(d)))...), d...)
}
func f32(num int, f float32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, math.Float32bits(f))
	return append(vi(uint64(num<<3|5)), b...)
}
func f64(num int, f float64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, math.Float64bits(f))
	return append(vi(uint64(num<<3|1)), b...)
}
func cat(parts ...[]byte) []byte {
	var o []byte
	for _, p := range parts {
		o = append(o, p...)
	}
	return o
}
func metric(name string, alias uint64, dt uint64, value []byte) []byte {
	m := []byte{}
	if name != "" {
		m = cat(m, fb(1, []byte(name)))
	}
	if alias != 0 {
		m = cat(m, fv(2, alias))
	}
	return cat(m, fv(3, 1700000000000), fv(4, dt), value)
}

func TestParseTopic(t *testing.T) {
	good := map[string]Topic{
		"spBv1.0/plant/NBIRTH/edge1":      {"plant", "NBIRTH", "edge1", ""},
		"spBv1.0/plant/DDATA/edge1/pump3": {"plant", "DDATA", "edge1", "pump3"},
	}
	for s, want := range good {
		if got, err := ParseTopic(s); err != nil || got != want {
			t.Errorf("%s: %v %v", s, got, err)
		}
	}
	for _, s := range []string{"", "spBv1.0/a/NDATA", "spBv1.0/a/NDATA/e/d", "spBv1.0/a/DDATA/e", "spBv1.0/a/XXX/e", "spBv1.0/+/NDATA/e", "spBv1.0/a//e", "t/a/g/b/telemetry", "spBv1.0/STATE/host"} {
		if _, err := ParseTopic(s); err == nil {
			t.Errorf("%q accepted", s)
		}
	}
}

func TestDecodeTypes(t *testing.T) {
	neg := uint64(uint32(0xFFFFFFF6)) // int32 -10 as uint32
	pl := cat(fv(1, 1700000000123), fv(3, 7),
		fb(2, metric("temp", 0, 9, f32(12, 21.5))),
		fb(2, metric("rpm", 0, 10, f64(13, 1480.25))),
		fb(2, metric("count", 0, 8, fv(11, 4000000000))),
		fb(2, metric("delta", 0, 3, fv(10, neg))),
		fb(2, metric("run", 0, 11, fv(14, 1))),
		fb(2, metric("name", 0, 12, fb(15, []byte("pump")))),
		fb(2, metric("bad", 0, 9, f32(12, float32(math.NaN())))),
	)
	p, err := Decode(pl)
	if err != nil || p.Seq != 7 || p.TimestampMs != 1700000000123 || len(p.Metrics) != 7 {
		t.Fatalf("%v %+v", err, p)
	}
	want := []struct {
		v float64
		n bool
	}{{21.5, true}, {1480.25, true}, {4e9, true}, {-10, true}, {1, true}, {0, false}, {0, false}}
	for i, w := range want {
		if p.Metrics[i].Numeric != w.n || (w.n && p.Metrics[i].Value != w.v) {
			t.Errorf("metric %d (%s): %+v", i, p.Metrics[i].Name, p.Metrics[i])
		}
	}
}

func TestAliasesAndDeath(t *testing.T) {
	var a Aliases
	topic := Topic{"g", "DBIRTH", "e", "d"}
	birth, _ := Decode(cat(fb(2, metric("temp", 5, 9, f32(12, 1))), fb(2, metric("rpm", 6, 9, f32(12, 2)))))
	if got := a.Resolve(topic, birth); len(got) != 2 {
		t.Fatalf("birth %v", got)
	}
	data, _ := Decode(cat(fb(2, metric("", 6, 9, f32(12, 1500))), fb(2, metric("", 99, 9, f32(12, 1)))))
	got := a.Resolve(Topic{"g", "DDATA", "e", "d"}, data)
	if len(got) != 1 || got[0].Name != "rpm" || got[0].Value != 1500 {
		t.Fatalf("alias resolve %+v", got)
	}
	// another device's aliases are separate
	if got := a.Resolve(Topic{"g", "DDATA", "e", "other"}, data); len(got) != 0 {
		t.Fatalf("alias leaked across devices: %+v", got)
	}
	a.Resolve(Topic{"g", "DDEATH", "e", "d"}, &Payload{})
	if got := a.Resolve(Topic{"g", "DDATA", "e", "d"}, data); len(got) != 0 {
		t.Fatalf("aliases survived death: %+v", got)
	}
}

func TestDecodeMalformed(t *testing.T) {
	good := cat(fv(3, 1), fb(2, metric("t", 0, 9, f32(12, 1))))
	for i := 0; i < len(good); i++ { // every truncation must error or succeed, never panic
		Decode(good[:i])
	}
	for _, b := range [][]byte{{0xFF}, {0x12, 0x05, 0x01}, {0x0B}, {0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01}} {
		if _, err := Decode(b); err == nil {
			t.Errorf("%x accepted", b)
		}
	}
	if _, err := Decode(make([]byte, 1<<20+1)); err == nil {
		t.Error("oversize accepted")
	}
}
