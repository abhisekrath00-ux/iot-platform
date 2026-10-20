package driver

// Decode-path verification for the ABB M4M 20/30 template
// (docs/devices/abb-m4m.md): FC 03 (holding registers, unlike most meters),
// scaled integers, word order abcd. Synthetic raw registers worked out from
// the documented scales - this proves our decode matches the template's
// stated formats, not a hardware claim; the scales themselves stay behind
// the template's provisional confirmation gate.

import (
	"context"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

func TestM4MMapDecodesScaledIntegersOverFC03(t *testing.T) {
	putu32 := func(regs map[int]uint16, addr int, v uint32) {
		regs[addr] = uint16(v >> 16) // MSW first (abcd)
		regs[addr+1] = uint16(v)
	}
	regs := map[int]uint16{}
	putu32(regs, 23298, 2314) // v1n: 231.4 V (u32 x0.1)
	putu32(regs, 23312, 1523) // i1: 15.23 A (u32 x0.01)
	regs[23322] = 0xFFFD
	regs[23323] = 0xAD37        // p_total: -1522.65 W (i32 x0.01, export; two's complement of 152265)
	putu32(regs, 23338, 423100) // s_total: 4231.00 VA (i32 x0.01)
	regs[23346] = 5001          // freq: 50.01 Hz (u16 x0.01)
	regs[23360] = 0xFC65        // pf_total: -0.923 (i16 x0.001; two's complement of 923)
	fp := &fakePort{regs: regs}
	d := &modbusGeneric{port: fp, dev: config.Device{ID: "m4m", Address: 1, Points: []config.Point{
		{ID: "v1n", Register: 23298, Func: 3, Type: "u32", WordOrder: "abcd", Scale: 0.1, Unit: "V", Min: 0, Max: 1000},
		{ID: "i1", Register: 23312, Func: 3, Type: "u32", WordOrder: "abcd", Scale: 0.01, Unit: "A", Min: 0, Max: 100000},
		{ID: "p_total", Register: 23322, Func: 3, Type: "i32", WordOrder: "abcd", Scale: 0.01, Unit: "W", Min: -1e9, Max: 1e9},
		{ID: "s_total", Register: 23338, Func: 3, Type: "i32", WordOrder: "abcd", Scale: 0.01, Unit: "VA", Min: 0, Max: 1e9},
		{ID: "freq", Register: 23346, Func: 3, Type: "u16", WordOrder: "abcd", Scale: 0.01, Unit: "Hz", Min: 40, Max: 70},
		{ID: "pf_total", Register: 23360, Func: 3, Type: "i16", WordOrder: "abcd", Scale: 0.001, Unit: "", Min: -1, Max: 1},
	}}}
	got, err := d.Poll(context.Background())
	if err != nil || len(got) != 6 {
		t.Fatalf("poll: %v %d", err, len(got))
	}
	if fp.sawFn != 0x03 {
		t.Errorf("function code = %#x, want 0x03 (holding registers)", fp.sawFn)
	}
	want := map[string]float64{"v1n": 231.4, "i1": 15.23, "p_total": -1522.65, "s_total": 4231.0, "freq": 50.01, "pf_total": -0.923}
	for _, g := range got {
		w := want[g.PointID]
		if d := g.Value - w; d > 1e-6 || d < -1e-6 {
			t.Errorf("%s = %v want %v", g.PointID, g.Value, w)
		}
	}
}
