package driver

// Decode-path verification for the Iskra MC 7x0 template
// (docs/devices/iskra-mc7x0.md): FC 04 input registers, Modicon 3xxxx
// notation (on-wire = listed - 30001) and the T5/T6 decade-exponent encoding
// are vendor-stated (MC 7x0 User's Manual Ver 1.01 Appendix A; type decoding
// cross-checked in the MC 3x0 manual's "Data types decoding", whose register
// table lists identical addresses and type codes for frequency and total
// power). Raw registers below are worked from the vendor's own examples -
// this proves our decode matches the template's stated formats, not a
// hardware claim.

import (
	"context"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

func TestMC7x0MapDecodesDecadeExponentOverFC04(t *testing.T) {
	// putT5/puTT6 encode mantissa x 10^exp MSW-first (abcd), as in the
	// vendor's worked example FD01 E240 = 123456 x 10^-3.
	putT := func(regs map[int]uint16, addr int, mant int32, exp int) {
		u := uint32(mant) & 0xFFFFFF
		w := (uint32(int8(exp)) << 24) | u
		regs[addr] = uint16(w >> 16)
		regs[addr+1] = uint16(w)
	}
	regs := map[int]uint16{}
	putT(regs, 104, 5003, -2)   // freq (30105): 50.03 Hz
	putT(regs, 106, 2304, -1)   // v1 (30107): 230.4 V
	putT(regs, 125, 5123, -3)   // i1 (30126): 5.123 A
	putT(regs, 139, 152345, -1) // p_total (30140): 15234.5 W
	putT(regs, 147, -4212, 0)   // q_total (30148): -4212 var (export)
	putT(regs, 155, 16789, -1)  // s_total (30156): 1678.9 VA
	regs[181] = 134             // thd_u1 (30182): T16, 1.34 %
	fp := &fakePort{regs: regs}
	d := &modbusGeneric{port: fp, dev: config.Device{ID: "mc7x0", Address: 33, Points: []config.Point{
		{ID: "freq", Register: 104, Func: 4, Type: "t5", WordOrder: "abcd", Scale: 1, Unit: "Hz", Min: 40, Max: 70},
		{ID: "v1", Register: 106, Func: 4, Type: "t5", WordOrder: "abcd", Scale: 1, Unit: "V", Min: 0, Max: 1000},
		{ID: "i1", Register: 125, Func: 4, Type: "t5", WordOrder: "abcd", Scale: 1, Unit: "A", Min: 0, Max: 10000},
		{ID: "p_total", Register: 139, Func: 4, Type: "t6", WordOrder: "abcd", Scale: 1, Unit: "W", Min: -1e9, Max: 1e9},
		{ID: "q_total", Register: 147, Func: 4, Type: "t6", WordOrder: "abcd", Scale: 1, Unit: "var", Min: -1e9, Max: 1e9},
		{ID: "s_total", Register: 155, Func: 4, Type: "t5", WordOrder: "abcd", Scale: 1, Unit: "VA", Min: 0, Max: 1e9},
		{ID: "thd_u1", Register: 181, Func: 4, Type: "u16", WordOrder: "abcd", Scale: 0.01, Unit: "%", Min: 0, Max: 100},
	}}}
	got, err := d.Poll(context.Background())
	if err != nil || len(got) != 7 {
		t.Fatalf("poll: %v %d", err, len(got))
	}
	if fp.sawFn != 0x04 {
		t.Errorf("function code = %#x, want 0x04 (input registers, vendor-stated)", fp.sawFn)
	}
	want := map[string]float64{
		"freq": 50.03, "v1": 230.4, "i1": 5.123, "p_total": 15234.5,
		"q_total": -4212, "s_total": 1678.9, "thd_u1": 1.34,
	}
	for _, g := range got {
		w := want[g.PointID]
		if d := g.Value - w; d > 1e-2 || d < -1e-2 {
			t.Errorf("%s = %v want %v", g.PointID, g.Value, w)
		}
	}
}

func TestMC7x0TypesRejectedOnWritePath(t *testing.T) {
	// t5/t6 are read-only measurement encodings; the write path must fail
	// clearly instead of framing a bogus register write.
	fp := &fakePort{regs: map[int]uint16{}}
	d := writerFor(fp, config.Point{ID: "p_total", Register: 139, Func: 16, Type: "t6", WordOrder: "abcd", Min: -10, Max: 10})
	if err := d.Write(context.Background(), "p_total", 1); err == nil {
		t.Fatal("t6 write accepted; read-only encodings must be rejected")
	}
}
