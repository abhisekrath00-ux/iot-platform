package driver

// Decode-path verification for the scaled-integer templates added with the
// Socomec DIRIS A-40 map (docs/devices/socomec-diris-a40.md). The points below
// mirror the built-in web template (socomec-diris-a40) exactly as deviceYaml
// emits them: FC 04, u32/i32/i16, word order abcd, per-point scales. Synthetic
// raw registers only - no hardware claim.

import (
	"context"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

func TestDIRISA40MapDecodesScaledIntegers(t *testing.T) {
	put32 := func(regs map[int]uint16, addr int, v uint32) {
		regs[addr] = uint16(v >> 16) // MSW first (abcd)
		regs[addr+1] = uint16(v)
	}
	regs := map[int]uint16{}
	put32(regs, 18444, 23050)      // v1n: 230.50 V at scale 0.01
	put32(regs, 18458, 4250)       // i1: 4.250 A at scale 0.001
	put32(regs, 18442, 49980)      // freq: 49.980 Hz at scale 0.001
	put32(regs, 18476, 0xFFFFC486) // p_total: S32 -15226 W at scale 1
	regs[18518] = uint16(0xFC25)   // pf1: S16 -987 -> -0.987 at scale 0.001
	fp := &fakePort{regs: regs}
	d := &modbusGeneric{port: fp, dev: config.Device{ID: "diris-a40-l1", Address: 1, Points: []config.Point{
		{ID: "v1n", Register: 18444, Func: 4, Type: "u32", WordOrder: "abcd", Scale: 0.01, Unit: "V", Min: 0, Max: 1000},
		{ID: "i1", Register: 18458, Func: 4, Type: "u32", WordOrder: "abcd", Scale: 0.001, Unit: "A", Min: 0, Max: 100000},
		{ID: "freq", Register: 18442, Func: 4, Type: "u32", WordOrder: "abcd", Scale: 0.001, Unit: "Hz", Min: 0, Max: 70},
		{ID: "p_total", Register: 18476, Func: 4, Type: "i32", WordOrder: "abcd", Scale: 1, Unit: "W", Min: -1e9, Max: 1e9},
		{ID: "pf1", Register: 18518, Func: 4, Type: "i16", WordOrder: "abcd", Scale: 0.001, Unit: "", Min: -1, Max: 1},
	}}}
	got, err := d.Poll(context.Background())
	if err != nil || len(got) != 5 {
		t.Fatalf("poll: %v %d", err, len(got))
	}
	want := map[string]float64{"v1n": 230.50, "i1": 4.25, "freq": 49.98, "p_total": -15226, "pf1": -0.987}
	for _, g := range got {
		w := want[g.PointID]
		if d := g.Value - w; d > 1e-9 || d < -1e-9 {
			t.Errorf("%s = %v want %v", g.PointID, g.Value, w)
		}
	}
}
