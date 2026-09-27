package driver

import (
	"math"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"testing"
)

func TestDecodeWords(t *testing.T) {
	f100 := math.Float32bits(100.0) // 0x42C80000
	cases := []struct {
		name       string
		words      []uint16
		typ, order string
		want       float64
	}{
		{"u16", []uint16{0x1234}, "u16", "", 0x1234},
		{"i16 negative", []uint16{0xFFFF}, "i16", "", -1},
		{"u32 abcd", []uint16{0x0001, 0x0000}, "u32", "abcd", 65536},
		{"u32 cdab", []uint16{0x0000, 0x0001}, "u32", "cdab", 65536},
		{"u32 badc", []uint16{0x0100, 0x0000}, "u32", "badc", 65536},
		{"i32 negative", []uint16{0xFFFF, 0xFFFF}, "i32", "abcd", -1},
		{"f32 abcd", []uint16{uint16(f100 >> 16), uint16(f100)}, "f32", "abcd", 100},
		{"f32 cdab", []uint16{uint16(f100), uint16(f100 >> 16)}, "f32", "cdab", 100},
		{"f32 badc", []uint16{0xC842, 0x0000}, "f32", "badc", 100},
		{"f32 dcba", []uint16{0x0000, 0xC842}, "f32", "dcba", 100},
	}
	for _, c := range cases {
		got, err := decodeWords(c.words, c.typ, c.order)
		if err != nil || got != c.want {
			t.Errorf("%s: got %v err %v, want %v", c.name, got, err, c.want)
		}
	}
	if _, err := decodeWords([]uint16{0}, "u24", ""); err == nil {
		t.Error("unknown type accepted")
	}
	if _, err := decodeWords([]uint16{0}, "u16", "xyz"); err == nil {
		t.Error("unknown word order accepted")
	}
}

func TestPointDefaults(t *testing.T) {
	p := config.Point{ID: "kwh", Register: 1}
	if f := pointFunc(p); f != 4 {
		t.Fatalf("legacy default func %d", f)
	}
	if ty := pointType(p); ty != "u16" {
		t.Fatalf("legacy default type %q", ty)
	}
	p2 := config.Point{ID: "door", Func: 2}
	if ty := pointType(p2); ty != "bool" {
		t.Fatalf("discrete default type %q", ty)
	}
}
