package driver

// Decode-path verification for the Eastron SDM72CT-M template
// (docs/devices/eastron-sdm72ct-m.md): FC 04 and wire addresses vendor-stated
// (1_SDM72CT-M_Protocol_V1.1.pdf); word order is not stated (the
// manufacturer's sibling SDM72D-M manual says the byte order varies per
// unit), so it stays behind the template's provisional gate. Synthetic raw
// registers worked out from IEEE-754 bit patterns - this proves our decode
// matches the template's stated formats, not a hardware claim.

import (
	"context"
	"math"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

func TestSDM72CTMapDecodesFloatOverFC04(t *testing.T) {
	putf32 := func(regs map[int]uint16, addr int, f float32) {
		bits := math.Float32bits(f)
		regs[addr] = uint16(bits >> 16) // MSW first (abcd - the gate's choice, not vendor fact)
		regs[addr+1] = uint16(bits)
	}
	regs := map[int]uint16{}
	putf32(regs, 0x0000, 230.1)   // v1n: 230.1 V
	putf32(regs, 0x0006, 5.67)    // i1: 5.67 A
	putf32(regs, 0x0034, -1234.5) // p_total: -1234.5 W (export; negative float)
	putf32(regs, 0x003E, -0.92)   // pf_total: -0.92 (reverse current per vendor note 1)
	putf32(regs, 0x0046, 49.98)   // freq: 49.98 Hz
	putf32(regs, 0x0156, 54321.0) // kwh_total: 54321.0 kWh
	fp := &fakePort{regs: regs}
	d := &modbusGeneric{port: fp, dev: config.Device{ID: "sdm72ct", Address: 1, Points: []config.Point{
		{ID: "v1n", Register: 0x0000, Func: 4, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "V", Min: 0, Max: 1000},
		{ID: "i1", Register: 0x0006, Func: 4, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "A", Min: 0, Max: 10000},
		{ID: "p_total", Register: 0x0034, Func: 4, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "W", Min: -1e8, Max: 1e8},
		{ID: "pf_total", Register: 0x003E, Func: 4, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "", Min: -1, Max: 1},
		{ID: "freq", Register: 0x0046, Func: 4, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "Hz", Min: 40, Max: 70},
		{ID: "kwh_total", Register: 0x0156, Func: 4, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "kWh", Min: 0, Max: 1e12},
	}}}
	got, err := d.Poll(context.Background())
	if err != nil || len(got) != 6 {
		t.Fatalf("poll: %v %d", err, len(got))
	}
	if fp.sawFn != 0x04 {
		t.Errorf("function code = %#x, want 0x04 (input registers, vendor-stated)", fp.sawFn)
	}
	want := map[string]float64{"v1n": 230.1, "i1": 5.67, "p_total": -1234.5, "pf_total": -0.92, "freq": 49.98, "kwh_total": 54321.0}
	for _, g := range got {
		w := want[g.PointID]
		if d := g.Value - w; d > 1e-2 || d < -1e-2 {
			t.Errorf("%s = %v want %v", g.PointID, g.Value, w)
		}
	}
}
