package driver

// Decode-path verification for the Accuenergy Acuvim II template
// (docs/devices/accuenergy-acuvim2.md): FC 03 is vendor-stated per sheet
// header ("03H Read"); import energy is UINT32 with Ep=Rx/10 per the Energy
// sheet; the manual states the dword layout (high word first) but not the
// float word order, which stays behind the template's provisional gate.
// Synthetic raw registers worked out from IEEE-754 bit patterns - this
// proves our decode matches the template's stated formats, not a hardware
// claim.

import (
	"context"
	"math"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

func TestAcuvim2MapDecodesFloatAndScaledU32OverFC03(t *testing.T) {
	putf32 := func(regs map[int]uint16, addr int, f float32) {
		bits := math.Float32bits(f)
		regs[addr] = uint16(bits >> 16) // MSW first (abcd - the gate's choice for floats)
		regs[addr+1] = uint16(bits)
	}
	regs := map[int]uint16{}
	putf32(regs, 0x4000, 50.01)  // freq: 50.01 Hz
	putf32(regs, 0x4002, 231.2)  // v1n: 231.2 V
	putf32(regs, 0x4012, 87.65)  // i1: 87.65 A
	putf32(regs, 0x4022, -42.75) // p_total: -42.75 kW (export; negative float)
	putf32(regs, 0x403A, 0.95)   // pf_total: 0.95
	regs[0x4048] = 0x002D        // kwh_import raw UINT32 2976327 -> 297632.7 kWh at scale 0.1
	regs[0x4049] = 0x6A47        // (vendor-stated dword layout: high word first)
	fp := &fakePort{regs: regs}
	d := &modbusGeneric{port: fp, dev: config.Device{ID: "acuvim2", Address: 1, Points: []config.Point{
		{ID: "freq", Register: 0x4000, Func: 3, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "Hz", Min: 40, Max: 70},
		{ID: "v1n", Register: 0x4002, Func: 3, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "V", Min: 0, Max: 1000},
		{ID: "i1", Register: 0x4012, Func: 3, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "A", Min: 0, Max: 10000},
		{ID: "p_total", Register: 0x4022, Func: 3, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "kW", Min: -1e7, Max: 1e7},
		{ID: "pf_total", Register: 0x403A, Func: 3, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "", Min: -1, Max: 1},
		{ID: "kwh_import", Register: 0x4048, Func: 3, Type: "u32", WordOrder: "abcd", Scale: 0.1, Unit: "kWh", Min: 0, Max: 99999999.9},
	}}}
	got, err := d.Poll(context.Background())
	if err != nil || len(got) != 6 {
		t.Fatalf("poll: %v %d", err, len(got))
	}
	if fp.sawFn != 0x03 {
		t.Errorf("function code = %#x, want 0x03 (vendor-stated)", fp.sawFn)
	}
	want := map[string]float64{"freq": 50.01, "v1n": 231.2, "i1": 87.65, "p_total": -42.75, "pf_total": 0.95, "kwh_import": 297632.7}
	for _, g := range got {
		w := want[g.PointID]
		if d := g.Value - w; d > 1e-2 || d < -1e-2 {
			t.Errorf("%s = %v want %v", g.PointID, g.Value, w)
		}
	}
}
