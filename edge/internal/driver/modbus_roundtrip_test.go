package driver

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"go.bug.st/serial"
	"gopkg.in/yaml.v3"
)

// fakePort answers Modbus requests from a register map, computing CRCs like a
// real RTU device. It lets the full frame path (request build, CRC verify,
// byte-count check, decode) run without hardware.
type fakePort struct {
	regs      map[int]uint16 // input/holding registers
	coils     map[int]bool
	sawAddr   byte
	sawFn     byte
	gotReq    []byte
	readBuf   *bytes.Reader
	corrupt   bool
	writes    [][]byte
	exception byte
	badEcho   bool
}

func (f *fakePort) SetMode(*serial.Mode) error         { return nil }
func (f *fakePort) Drain() error                       { return nil }
func (f *fakePort) ResetInputBuffer() error            { return nil }
func (f *fakePort) SetReadTimeout(time.Duration) error { return nil }
func (f *fakePort) Break(time.Duration) error          { return nil }
func (f *fakePort) Close() error                       { return nil }
func (f *fakePort) SetRTS(bool) error                  { return nil }
func (f *fakePort) ResetOutputBuffer() error           { return nil }
func (f *fakePort) SetDTR(bool) error                  { return nil }
func (f *fakePort) GetModemStatusBits() (*serial.ModemStatusBits, error) {
	return &serial.ModemStatusBits{}, nil
}

func (f *fakePort) Write(p []byte) (int, error) {
	f.gotReq = append([]byte(nil), p...)
	addr, fn := p[0], p[1]
	reg := int(binary.BigEndian.Uint16(p[2:4]))
	count := int(binary.BigEndian.Uint16(p[4:6]))
	f.sawAddr, f.sawFn = addr, fn
	if fn == 5 || fn == 6 || fn == 16 {
		f.writes = append(f.writes, append([]byte(nil), p...))
		if fn == 6 {
			f.regs[reg] = uint16(count)
		}
		if fn == 16 {
			for i := 0; i < count; i++ {
				f.regs[reg+i] = binary.BigEndian.Uint16(p[7+2*i:])
			}
		}
		if f.exception != 0 {
			resp := []byte{addr, fn | 0x80, f.exception}
			c := crc16(resp)
			f.readBuf = bytes.NewReader(append(resp, byte(c), byte(c>>8)))
			return len(p), nil
		}
		resp := append([]byte{addr}, p[1:6]...)
		if f.badEcho {
			resp[5] ^= 1
		}
		c := crc16(resp)
		f.readBuf = bytes.NewReader(append(resp, byte(c), byte(c>>8)))
		return len(p), nil
	}
	var data []byte
	switch fn {
	case 1, 2:
		var b byte
		for i := 0; i < count; i++ {
			if f.coils[reg+i] {
				b |= 1 << i
			}
		}
		data = []byte{b}
	default:
		for i := 0; i < count; i++ {
			w := f.regs[reg+i]
			data = append(data, byte(w>>8), byte(w))
		}
	}
	resp := []byte{addr, fn, byte(len(data))}
	resp = append(resp, data...)
	c := crc16(resp)
	resp = append(resp, byte(c), byte(c>>8))
	if f.corrupt {
		resp[3] ^= 0xFF
	}
	f.readBuf = bytes.NewReader(resp)
	return len(p), nil
}

func (f *fakePort) Read(p []byte) (int, error) {
	if f.readBuf == nil {
		return 0, nil
	}
	return f.readBuf.Read(p)
}

func TestGenericDriverRoundTrip(t *testing.T) {
	fp := &fakePort{
		regs:  map[int]uint16{10: 0x42C8, 11: 0x0000, 20: 0x00FA},
		coils: map[int]bool{5: true},
	}
	d := &modbusGeneric{port: fp, dev: config.Device{ID: "s1", Address: 7, Points: []config.Point{
		{ID: "temp", Register: 10, Func: 4, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "C", Min: -50, Max: 150},
		{ID: "count", Register: 20, Func: 3, Type: "u16", Scale: 0.1, Unit: "l", Min: 0, Max: 100},
		{ID: "valve", Register: 5, Func: 1, Min: 0, Max: 1},
	}}}
	readings, err := d.Poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(readings) != 3 {
		t.Fatalf("readings=%d", len(readings))
	}
	if readings[0].Value != 100 || readings[0].Unit != "C" {
		t.Fatalf("temp: %+v", readings[0])
	}
	if readings[1].Value != 25 { // 250 * 0.1
		t.Fatalf("count: %+v", readings[1])
	}
	if readings[2].Value != 1 {
		t.Fatalf("valve: %+v", readings[2])
	}
	if fp.sawAddr != 7 {
		t.Fatalf("addr %d", fp.sawAddr)
	}
	// request frame CRC must be correct
	req := fp.gotReq
	if crc16(req[:len(req)-2]) != binary.LittleEndian.Uint16(req[len(req)-2:]) {
		t.Fatal("request crc wrong")
	}
}

func TestGenericDriverRejectsCorruptFrame(t *testing.T) {
	fp := &fakePort{regs: map[int]uint16{10: 5}, corrupt: true}
	d := &modbusGeneric{port: fp, dev: config.Device{ID: "s1", Address: 1, Points: []config.Point{
		{ID: "x", Register: 10, Func: 4, Min: 0, Max: 100},
	}}}
	if _, err := d.Poll(context.Background()); err == nil {
		t.Fatal("corrupt frame accepted")
	}
}

func TestGenericDriverRejectsOutOfRange(t *testing.T) {
	fp := &fakePort{regs: map[int]uint16{10: 500}}
	d := &modbusGeneric{port: fp, dev: config.Device{ID: "s1", Address: 1, Points: []config.Point{
		{ID: "x", Register: 10, Func: 4, Min: 0, Max: 100},
	}}}
	if _, err := d.Poll(context.Background()); err == nil {
		t.Fatal("out-of-range value accepted")
	}
}

// Selec MX300-1-C-CE (single-phase meter) register map from its datasheet:
// input registers at protocol addresses 0x00.. (documented 30000..), float32,
// word order big endian (A-B-C-D, factory MSRF; the leaflet's own example box is inconsistent). Frame path only: this has
// not been run against a real meter.
func TestMX300MapDecodesABCD(t *testing.T) {
	put := func(regs map[int]uint16, addr int, v float32) {
		b := math.Float32bits(v)
		regs[addr] = uint16(b >> 16) // A-B word first (big endian, MSRF)
		regs[addr+1] = uint16(b)
	}
	regs := map[int]uint16{}
	put(regs, 0x00, 230.5) // voltage
	put(regs, 0x02, 4.25)  // current
	put(regs, 0x04, 0.9)   // active kW
	put(regs, 0x0A, 0.97)  // PF
	put(regs, 0x0C, 50.01) // frequency
	fp := &fakePort{regs: regs}
	pt := func(id string, reg int, unit string, max float64) config.Point {
		return config.Point{ID: id, Register: reg, Func: 4, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: unit, Min: 0, Max: max}
	}
	d := &modbusGeneric{port: fp, dev: config.Device{ID: "mx300", Address: 1, Points: []config.Point{
		pt("voltage", 0x00, "V", 600000), pt("current", 0x02, "A", 10000), pt("active_power", 0x04, "kW", 1e6),
		pt("power_factor", 0x0A, "", 1), pt("frequency", 0x0C, "Hz", 65),
	}}}
	got, err := d.Poll(context.Background())
	if err != nil || len(got) != 5 {
		t.Fatalf("poll: %v %d", err, len(got))
	}
	want := []float64{230.5, 4.25, 0.9, 0.97, 50.01}
	for i, w := range want {
		if d := got[i].Value - w; d > 1e-4 || d < -1e-4 {
			t.Errorf("%s = %v want %v", got[i].PointID, got[i].Value, w)
		}
	}
}

// Selec's generic example box: 1234.12 = float32 0x449A43D7, stored either as
// 0x449A,0x43D7 (big endian) or 0x43D7,0x449A (mid-little). Both decode.
func TestSelecWordOrderExample(t *testing.T) {
	for order, regs := range map[string]map[int]uint16{"abcd": {90: 0x449A, 91: 0x43D7}, "cdab": {90: 0x43D7, 91: 0x449A}} {
		fp := &fakePort{regs: regs}
		d := &modbusGeneric{port: fp, dev: config.Device{ID: "m", Address: 1, Points: []config.Point{
			{ID: "e", Register: 90, Func: 4, Type: "f32", WordOrder: order, Scale: 1, Unit: "kWh", Min: 0, Max: 1e9},
		}}}
		got, err := d.Poll(context.Background())
		if err != nil || len(got) != 1 {
			t.Fatalf("%s: %v %d", order, err, len(got))
		}
		if d := got[0].Value - 1234.12; d > 0.01 || d < -0.01 {
			t.Fatalf("%s: got %v", order, got[0].Value)
		}
	}
}

// The MFM383A-C profile in docs/devices is parsed and exercised, so the doc
// cannot drift from what the driver accepts.
func TestMFM383ADocProfile(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/devices/selec-mfm383a-c.md")
	if err != nil {
		t.Skip("doc not found")
	}
	m := regexp.MustCompile("(?s)```yaml\n(.*?)```").FindSubmatch(raw)
	if m == nil {
		t.Fatal("no yaml block")
	}
	var devs []config.Device
	if err := yaml.Unmarshal(m[1], &devs); err != nil {
		t.Fatal(err)
	}
	if len(devs) != 1 || len(devs[0].Points) != 32 {
		t.Fatalf("devices=%d points=%d", len(devs), len(devs[0].Points))
	}
	regs := map[int]uint16{}
	for i, p := range devs[0].Points {
		var v float32 = float32(i + 1)
		if p.Unit == "" {
			v = 0.95
		}
		if p.ID == "freq" {
			v = 50
		}
		b := math.Float32bits(v)
		regs[p.Register], regs[p.Register+1] = uint16(b>>16), uint16(b)
	}
	d := &modbusGeneric{port: &fakePort{regs: regs}, dev: devs[0]}
	got, err := d.Poll(context.Background())
	if err != nil || len(got) != 32 {
		t.Fatalf("poll: %v %d", err, len(got))
	}
	if got[28].PointID != "freq" || got[28].Value != 50 || got[0].PointID != "v1n" || got[0].Value != 1 {
		t.Fatalf("decoded %+v %+v", got[0], got[28])
	}
}
