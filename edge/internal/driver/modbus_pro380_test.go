package driver

// Decode-path verification for the Inepro PRO380-Mod template
// (docs/devices/inepro-pro380.md): FC 03, Float ABCD, listed address =
// on-wire address - all three stated in Inepro's own manual V2.18-8,
// Appendix A3.2. Synthetic raw registers worked out from IEEE-754 bit
// patterns - this proves our decode matches the template's stated formats,
// not a hardware claim.

import (
	"context"
	"math"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

func TestPRO380MapDecodesFloatABCDOverFC03(t *testing.T) {
	putf32 := func(regs map[int]uint16, addr int, f float32) {
		bits := math.Float32bits(f)
		regs[addr] = uint16(bits >> 16) // MSW first (ABCD, vendor-stated)
		regs[addr+1] = uint16(bits)
	}
	regs := map[int]uint16{}
	putf32(regs, 0x5002, 230.7)   // v1n: 230.7 V
	putf32(regs, 0x5008, 50.02)   // freq: 50.02 Hz
	putf32(regs, 0x500C, 12.34)   // i1: 12.34 A
	putf32(regs, 0x5012, -845.5)  // p_total: -845.5 kW (export; negative float)
	putf32(regs, 0x502A, 0.987)   // pf_total: 0.987
	putf32(regs, 0x600C, 12345.6) // kwh_import (forward active energy): 12345.6 kWh
	fp := &fakePort{regs: regs}
	d := &modbusGeneric{port: fp, dev: config.Device{ID: "pro380", Address: 1, Points: []config.Point{
		{ID: "v1n", Register: 0x5002, Func: 3, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "V", Min: 0, Max: 500},
		{ID: "freq", Register: 0x5008, Func: 3, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "Hz", Min: 40, Max: 70},
		{ID: "i1", Register: 0x500C, Func: 3, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "A", Min: 0, Max: 10000},
		{ID: "p_total", Register: 0x5012, Func: 3, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "kW", Min: -1e7, Max: 1e7},
		{ID: "pf_total", Register: 0x502A, Func: 3, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "", Min: -1, Max: 1},
		{ID: "kwh_import", Register: 0x600C, Func: 3, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "kWh", Min: 0, Max: 1e12},
	}}}
	got, err := d.Poll(context.Background())
	if err != nil || len(got) != 6 {
		t.Fatalf("poll: %v %d", err, len(got))
	}
	if fp.sawFn != 0x03 {
		t.Errorf("function code = %#x, want 0x03 (holding registers, vendor-stated)", fp.sawFn)
	}
	want := map[string]float64{"v1n": 230.7, "freq": 50.02, "i1": 12.34, "p_total": -845.5, "pf_total": 0.987, "kwh_import": 12345.6}
	for _, g := range got {
		w := want[g.PointID]
		if d := g.Value - w; d > 1e-3 || d < -1e-3 {
			t.Errorf("%s = %v want %v", g.PointID, g.Value, w)
		}
	}
}
