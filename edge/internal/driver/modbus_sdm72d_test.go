package driver

// Decode-path verification for the Eastron SDM72D-M template
// (docs/devices/eastron-sdm72d-m.md): FC 04 input registers and the wire
// addresses are vendor-stated (Eastron SDM72D-M-1 User Manual V1.4, Part 3);
// the manual says the Hi/Lo byte order "is set automatically to normal or
// reverse", so word order stays behind the template's provisional gate.
// Synthetic raw registers worked out from IEEE-754 bit patterns - this
// proves our decode matches the template's stated formats, not a hardware
// claim.

import (
	"context"
	"math"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

func TestSDM72DMapDecodesFloatOverFC04(t *testing.T) {
	putf32 := func(regs map[int]uint16, addr int, f float32) {
		bits := math.Float32bits(f)
		regs[addr] = uint16(bits >> 16) // MSW first (abcd - the gate's choice, not vendor fact)
		regs[addr+1] = uint16(bits)
	}
	regs := map[int]uint16{}
	putf32(regs, 0x0034, 15234.5)  // p_total: 15234.5 W
	putf32(regs, 0x000C, 5123.25)  // p1: 5123.25 W
	putf32(regs, 0x0048, 98765.4)  // kwh_import: 98765.4 kWh
	putf32(regs, 0x0156, 101112.0) // kwh_total
	putf32(regs, 0x0500, 12000.0)  // p_import: 12000 W
	fp := &fakePort{regs: regs}
	d := &modbusGeneric{port: fp, dev: config.Device{ID: "sdm72", Address: 1, Points: []config.Point{
		{ID: "p1", Register: 0x000C, Func: 4, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "W", Min: -1e8, Max: 1e8},
		{ID: "p_total", Register: 0x0034, Func: 4, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "W", Min: -1e8, Max: 1e8},
		{ID: "kwh_import", Register: 0x0048, Func: 4, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "kWh", Min: 0, Max: 1e12},
		{ID: "kwh_total", Register: 0x0156, Func: 4, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "kWh", Min: 0, Max: 1e12},
		{ID: "p_import", Register: 0x0500, Func: 4, Type: "f32", WordOrder: "abcd", Scale: 1, Unit: "W", Min: 0, Max: 1e8},
	}}}
	got, err := d.Poll(context.Background())
	if err != nil || len(got) != 5 {
		t.Fatalf("poll: %v %d", err, len(got))
	}
	if fp.sawFn != 0x04 {
		t.Errorf("function code = %#x, want 0x04 (input registers, vendor-stated)", fp.sawFn)
	}
	want := map[string]float64{"p1": 5123.25, "p_total": 15234.5, "kwh_import": 98765.4, "kwh_total": 101112.0, "p_import": 12000.0}
	for _, g := range got {
		w := want[g.PointID]
		if d := g.Value - w; d > 1e-2 || d < -1e-2 {
			t.Errorf("%s = %v want %v", g.PointID, g.Value, w)
		}
	}
}
