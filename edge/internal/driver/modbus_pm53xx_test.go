package driver

// Decode-path verification for the Schneider PowerLogic PM53xx template
// (docs/devices/schneider-pm53xx.md): FC 03, FLOAT32 holding registers,
// Schneider's four-quadrant PF encoding. Synthetic raw registers worked out
// from IEEE-754 bit patterns - this proves our decode matches the template's
// stated formats, not a hardware claim; word order, register base, and FC
// stay behind the template's provisional gates (Schneider's documents do
// not state them).

import (
	"context"
	"math"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

func TestPM53xxMapDecodesFloat32OverFC03(t *testing.T) {
	putf32 := func(regs map[int]uint16, addr int, f float32) {
		bits := math.Float32bits(f)
		regs[addr] = uint16(bits >> 16) // MSW first (abcd)
		regs[addr+1] = uint16(bits)
	}
	regs := map[int]uint16{}
	putf32(regs, 3028, 231.4)    // v1n: 231.4 V
	putf32(regs, 3010, 152.3)    // i_avg: 152.3 A
	putf32(regs, 3060, -1522.65) // p_total: -1522.65 kW (export; negative float)
	putf32(regs, 3084, -1.23)    // pf_total: 4Q encoding; -2<-1.23<-1 = Q2, |PF| = -2-(-1.23) decoded by consumers
	putf32(regs, 3110, 50.01)    // freq: 50.01 Hz
	putf32(regs, 2700, 42310.5)  // kwh_import: 42310.5 kWh
	fp := &fakePort{regs: regs}
	d := &modbusGeneric{port: fp, dev: config.Device{ID: "pm53", Address: 1, Points: []config.Point{
		{ID: "v1n", Register: 3028, Func: 3, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "V", Min: 0, Max: 1000},
		{ID: "i_avg", Register: 3010, Func: 3, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "A", Min: 0, Max: 10000},
		{ID: "p_total", Register: 3060, Func: 3, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "kW", Min: -1e7, Max: 1e7},
		{ID: "pf_total", Register: 3084, Func: 3, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "", Min: -2, Max: 2},
		{ID: "freq", Register: 3110, Func: 3, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "Hz", Min: 40, Max: 70},
		{ID: "kwh_import", Register: 2700, Func: 3, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "kWh", Min: 0, Max: 1e12},
	}}}
	got, err := d.Poll(context.Background())
	if err != nil || len(got) != 6 {
		t.Fatalf("poll: %v %d", err, len(got))
	}
	if fp.sawFn != 0x03 {
		t.Errorf("function code = %#x, want 0x03 (holding registers)", fp.sawFn)
	}
	want := map[string]float64{"v1n": 231.4, "i_avg": 152.3, "p_total": -1522.65, "pf_total": -1.23, "freq": 50.01, "kwh_import": 42310.5}
	for _, g := range got {
		w := want[g.PointID]
		if d := g.Value - w; d > 1e-3 || d < -1e-3 {
			t.Errorf("%s = %v want %v", g.PointID, g.Value, w)
		}
	}
}

// The installer-facing base choice: with "listed minus 1" the same physical
// quantity sits one register lower; both must decode identically.
func TestPM53xxBaseMinus1DecodesTheSame(t *testing.T) {
	regs := map[int]uint16{}
	bits := math.Float32bits(float32(50.01))
	regs[3109] = uint16(bits >> 16)
	regs[3110] = uint16(bits)
	fp := &fakePort{regs: regs}
	d := &modbusGeneric{port: fp, dev: config.Device{ID: "pm53", Address: 1, Points: []config.Point{
		{ID: "freq", Register: 3109, Func: 3, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "Hz", Min: 40, Max: 70},
	}}}
	got, err := d.Poll(context.Background())
	if err != nil || len(got) != 1 {
		t.Fatalf("poll: %v %d", err, len(got))
	}
	if v := got[0].Value; v < 50.009 || v > 50.011 {
		t.Errorf("freq = %v want 50.01 (register 3110 listed, read at 3109)", v)
	}
}
