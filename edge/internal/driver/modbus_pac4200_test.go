package driver

// Decode-path verification for the Siemens SENTRON PAC4200 template
// (docs/devices/siemens-pac4200.md, manual A5E02316180B-03 appendix A.3).
// The points below mirror the built-in web template (siemens-pac4200) exactly
// as deviceYaml emits them: FC 04, all f32, word order as chosen by the
// installer (abcd shown here). Synthetic raw registers only - Siemens does
// not state the word order, so this proves our decode matches the template's
// chosen order, not a hardware claim.

import (
	"context"
	"math"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

func TestPAC4200MapDecodesFloats(t *testing.T) {
	putf32 := func(regs map[int]uint16, addr int, v float32) {
		b := math.Float32bits(v)
		regs[addr] = uint16(b >> 16) // MSW first (abcd)
		regs[addr+1] = uint16(b)
	}
	regs := map[int]uint16{}
	putf32(regs, 1, 232.4)       // va_n: 232.4 V
	putf32(regs, 55, 50.02)      // freq: 50.02 Hz
	putf32(regs, 65, 18342.75)   // p_total: 18342.75 W
	putf32(regs, 2801, 987654.3) // energy_import_t1: 987654.3 Wh
	fp := &fakePort{regs: regs}
	d := &modbusGeneric{port: fp, dev: config.Device{ID: "pac4200", Address: 1, Points: []config.Point{
		{ID: "va_n", Register: 1, Func: 4, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "V", Min: 0, Max: 1000},
		{ID: "freq", Register: 55, Func: 4, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "Hz", Min: 45, Max: 65},
		{ID: "p_total", Register: 65, Func: 4, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "W", Min: -1e9, Max: 1e9},
		{ID: "energy_import_t1", Register: 2801, Func: 4, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "Wh", Min: 0, Max: 1e12},
	}}}
	got, err := d.Poll(context.Background())
	if err != nil || len(got) != 4 {
		t.Fatalf("poll: %v %d", err, len(got))
	}
	want := map[string]float64{"va_n": 232.4, "freq": 50.02, "p_total": 18342.75, "energy_import_t1": 987654.3}
	for _, g := range got {
		w := want[g.PointID]
		if diff := math.Abs(g.Value - w); diff > math.Max(0.01, math.Abs(w)*1e-6) { // f32 rounding on large counters
			t.Errorf("%s: got %v want %v", g.PointID, g.Value, w)
		}
	}
}

// The RS485 module manual's frame examples prove the listed offset is the
// on-wire PDU address (offset 50001 -> frame bytes C3 51). Guard the decode
// test above against any drift toward a -1 base.
func TestPAC4200ListedOffsetIsWireAddress(t *testing.T) {
	regs := map[int]uint16{}
	regs[55] = 0x4248 // 50.0f MSW
	regs[56] = 0x0000 // LSW
	fp := &fakePort{regs: regs}
	d := &modbusGeneric{port: fp, dev: config.Device{ID: "pac4200", Address: 1, Points: []config.Point{
		{ID: "freq", Register: 55, Func: 4, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "Hz", Min: 45, Max: 65},
	}}}
	got, err := d.Poll(context.Background())
	if err != nil || len(got) != 1 {
		t.Fatalf("poll: %v %d", err, len(got))
	}
	if math.Abs(got[0].Value-50.0) > 0.001 {
		t.Errorf("freq: got %v want 50.0 (register 55 must be read at PDU address 55)", got[0].Value)
	}
}
