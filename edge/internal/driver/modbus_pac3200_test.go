package driver

// Decode-path verification for the Siemens SENTRON PAC3200 template
// (docs/devices/siemens-pac3200.md, manual A5E01168664B-04 table 3-6). The
// points below mirror the built-in web template (siemens-pac3200) exactly as
// deviceYaml emits them: FC 04, f32 instantaneous and u32 counters plus f64
// energy counters, word order abcd. Synthetic raw registers only - the manual
// does not state the double word order, so this proves our decode matches the
// template's stated assumption, not a hardware claim.

import (
	"context"
	"math"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

func TestPAC3200MapDecodesFloatsAndDoubles(t *testing.T) {
	putf32 := func(regs map[int]uint16, addr int, v float32) {
		b := math.Float32bits(v)
		regs[addr] = uint16(b >> 16) // MSW first (abcd)
		regs[addr+1] = uint16(b)
	}
	putf64 := func(regs map[int]uint16, addr int, v float64) {
		b := math.Float64bits(v)
		for i := 0; i < 4; i++ {
			regs[addr+i] = uint16(b >> uint(48-16*i)) // MSW first (abcd)
		}
	}
	regs := map[int]uint16{}
	putf32(regs, 1, 231.75)     // va_n: 231.75 V
	putf32(regs, 65, 15226.5)   // p_total: 15.2265 kW in W
	putf64(regs, 801, 987654.3) // energy_import_t1: 987654.3 Wh
	regs[213] = 0x0000
	regs[214] = 0x4B0F // work_hours: 19215 s (u32)
	fp := &fakePort{regs: regs}
	d := &modbusGeneric{port: fp, dev: config.Device{ID: "pac3200", Address: 1, Points: []config.Point{
		{ID: "va_n", Register: 1, Func: 4, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "V", Min: 0, Max: 1000},
		{ID: "p_total", Register: 65, Func: 4, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "W", Min: -1e9, Max: 1e9},
		{ID: "work_hours", Register: 213, Func: 4, Type: "u32", WordOrder: "abcd", Scale: 1, Unit: "s", Min: 0, Max: 999999999},
		{ID: "energy_import_t1", Register: 801, Func: 4, Type: "f64", WordOrder: "abcd", Scale: 1, Unit: "Wh", Min: 0, Max: 1e12},
	}}}
	got, err := d.Poll(context.Background())
	if err != nil || len(got) != 4 {
		t.Fatalf("poll: %v %d", err, len(got))
	}
	want := map[string]float64{"va_n": 231.75, "p_total": 15226.5, "work_hours": 19215, "energy_import_t1": 987654.3}
	for _, g := range got {
		w := want[g.PointID]
		if d := g.Value - w; d > 1e-6 || d < -1e-6 {
			t.Errorf("%s = %v want %v", g.PointID, g.Value, w)
		}
	}
}
