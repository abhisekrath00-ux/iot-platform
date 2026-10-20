package driver

// Decode-path verification for the Carlo Gavazzi EM24-DIN E1 template
// (docs/devices/carlo-gavazzi-em24-e1.md): FC 03/04 equivalent, physical
// address = frame address, 32-bit word order LSW->MSW (cdab) - all stated in
// Carlo Gavazzi's EM24_E1_CP protocol document. All values are scaled
// integers. Synthetic raw registers - this proves our decode matches the
// template's stated formats, not a hardware claim.

import (
	"context"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

func TestEM24E1MapDecodesScaledIntsCDAB(t *testing.T) {
	puti32 := func(regs map[int]uint16, addr int, v int32) {
		regs[addr] = uint16(uint32(v)) // LSW first (cdab, vendor-stated)
		regs[addr+1] = uint16(uint32(v) >> 16)
	}
	regs := map[int]uint16{}
	puti32(regs, 0x0000, 2304)    // v1n: 230.4 V at scale 0.1
	puti32(regs, 0x000C, 42500)   // i1: 42.500 A at scale 0.001
	puti32(regs, 0x0028, -152260) // p_total: -15226.0 W at scale 0.1 (export)
	puti32(regs, 0x0034, 987654)  // kwh_import: 98765.4 kWh at scale 0.1
	regs[0x002E] = uint16(0xFC18) // pf1: INT16 -1000 -> -1.000 at scale 0.001 (lead per vendor note)
	regs[0x0033] = 500            // freq: UINT16 500 -> 50.0 Hz at scale 0.1
	fp := &fakePort{regs: regs}
	d := &modbusGeneric{port: fp, dev: config.Device{ID: "em24", Address: 1, Points: []config.Point{
		{ID: "v1n", Register: 0x0000, Func: 4, Type: "i32", WordOrder: "cdab", Scale: 0.1, Unit: "V", Min: 0, Max: 5000},
		{ID: "i1", Register: 0x000C, Func: 4, Type: "i32", WordOrder: "cdab", Scale: 0.001, Unit: "A", Min: 0, Max: 65000},
		{ID: "p_total", Register: 0x0028, Func: 4, Type: "i32", WordOrder: "cdab", Scale: 0.1, Unit: "W", Min: -2e8, Max: 2e8},
		{ID: "pf1", Register: 0x002E, Func: 4, Type: "i16", WordOrder: "cdab", Scale: 0.001, Unit: "", Min: -1, Max: 1},
		{ID: "freq", Register: 0x0033, Func: 4, Type: "u16", WordOrder: "cdab", Scale: 0.1, Unit: "Hz", Min: 40, Max: 70},
		{ID: "kwh_import", Register: 0x0034, Func: 4, Type: "i32", WordOrder: "cdab", Scale: 0.1, Unit: "kWh", Min: 0, Max: 2e8},
	}}}
	got, err := d.Poll(context.Background())
	if err != nil || len(got) != 6 {
		t.Fatalf("poll: %v %d", err, len(got))
	}
	if fp.sawFn != 0x04 {
		t.Errorf("function code = %#x, want 0x04", fp.sawFn)
	}
	want := map[string]float64{"v1n": 230.4, "i1": 42.5, "p_total": -15226.0, "pf1": -1.0, "freq": 50.0, "kwh_import": 98765.4}
	for _, g := range got {
		w := want[g.PointID]
		if d := g.Value - w; d > 1e-3 || d < -1e-3 {
			t.Errorf("%s = %v want %v", g.PointID, g.Value, w)
		}
	}
}
