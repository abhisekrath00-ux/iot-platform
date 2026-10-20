package driver

// Decode-path verification for the Carlo Gavazzi EM340 template
// (docs/devices/carlo-gavazzi-em340.md, EM300 series communication protocol
// CP). The points below mirror the built-in web template (carlo-gavazzi-em340)
// exactly as deviceYaml emits them: FC 04, INT32 scaled integers with LSW-first
// word order (cdab, as the protocol doc states) and i16 power factors.
// Synthetic raw registers only - no hardware claim.

import (
	"context"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

func TestEM340MapDecodesLswFirstScaledIntegers(t *testing.T) {
	put32lsw := func(regs map[int]uint16, addr int, v int32) {
		regs[addr] = uint16(uint32(v)) // LSW first (cdab)
		regs[addr+1] = uint16(uint32(v) >> 16)
	}
	regs := map[int]uint16{}
	put32lsw(regs, 0x0000, 2305)   // v1n: 230.5 V at weight Volt*10
	put32lsw(regs, 0x000C, 4250)   // i1: 4.250 A at weight Ampere*1000
	put32lsw(regs, 0x0012, -15226) // p1: -1522.6 W at weight Watt*10 (export)
	put32lsw(regs, 0x0034, 123456) // kwh_import: 12345.6 kWh at weight kWh*10
	regs[0x002E] = uint16(0xFC25)  // pf1: INT16 -987 -> -0.987 at weight PF*1000
	regs[0x0033] = 500             // freq: 50.0 Hz at weight Hz*10
	fp := &fakePort{regs: regs}
	d := &modbusGeneric{port: fp, dev: config.Device{ID: "em340", Address: 1, Points: []config.Point{
		{ID: "v1n", Register: 0x0000, Func: 4, Type: "i32", WordOrder: "cdab", Scale: 0.1, Unit: "V", Min: 0, Max: 1000},
		{ID: "i1", Register: 0x000C, Func: 4, Type: "i32", WordOrder: "cdab", Scale: 0.001, Unit: "A", Min: 0, Max: 100000},
		{ID: "p1", Register: 0x0012, Func: 4, Type: "i32", WordOrder: "cdab", Scale: 0.1, Unit: "W", Min: -1e9, Max: 1e9},
		{ID: "kwh_import", Register: 0x0034, Func: 4, Type: "i32", WordOrder: "cdab", Scale: 0.1, Unit: "kWh", Min: 0, Max: 1e12},
		{ID: "pf1", Register: 0x002E, Func: 4, Type: "i16", WordOrder: "cdab", Scale: 0.001, Unit: "", Min: -1, Max: 1},
		{ID: "freq", Register: 0x0033, Func: 4, Type: "i16", WordOrder: "cdab", Scale: 0.1, Unit: "Hz", Min: 0, Max: 70},
	}}}
	got, err := d.Poll(context.Background())
	if err != nil || len(got) != 6 {
		t.Fatalf("poll: %v %d", err, len(got))
	}
	want := map[string]float64{"v1n": 230.5, "i1": 4.25, "p1": -1522.6, "kwh_import": 12345.6, "pf1": -0.987, "freq": 50.0}
	for _, g := range got {
		w := want[g.PointID]
		if d := g.Value - w; d > 1e-9 || d < -1e-9 {
			t.Errorf("%s = %v want %v", g.PointID, g.Value, w)
		}
	}
}
