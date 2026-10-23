package rules

import (
	"math"
	"testing"
)

// A 0.002-degree square around (12.9, 77.6), about 220 m on a side.
func testSquare() []Vertex {
	return []Vertex{{12.899, 77.599}, {12.899, 77.601}, {12.901, 77.601}, {12.901, 77.599}}
}

func TestPointInPolygon(t *testing.T) {
	sq := testSquare()
	for _, c := range []struct {
		lat, lon float64
		want     bool
	}{
		{12.9, 77.6, true},       // centre
		{12.9009, 77.6009, true}, // near a corner, still inside
		{12.902, 77.6, false},    // north of the square
		{12.9, 77.598, false},    // west
		{12.898, 77.602, false},  // south-east, outside
		{12.8989, 77.6, false},   // a metre south of the southern edge
	} {
		if got := PointInPolygon(c.lat, c.lon, sq); got != c.want {
			t.Fatalf("PointInPolygon(%v,%v) = %v, want %v", c.lat, c.lon, got, c.want)
		}
	}
	// a concave polygon: the notch cuts the centre, so a point in the notch is out
	l := []Vertex{{0, 0}, {0, 4}, {4, 4}, {4, 0}, {3, 0}, {3, 3}, {1, 3}, {1, 0}}
	if PointInPolygon(2, 2, l) {
		t.Fatal("point inside the concave notch counted as inside")
	}
	if !PointInPolygon(0.5, 3.5, l) {
		t.Fatal("point in the solid part of a concave polygon counted as outside")
	}
}

func TestValidatePolygon(t *testing.T) {
	if err := ValidatePolygon(testSquare()); err != nil {
		t.Fatalf("square rejected: %v", err)
	}
	if err := ValidatePolygon([]Vertex{{0, 0}, {1, 1}}); err == nil {
		t.Fatal("2 vertices accepted")
	}
	big := make([]Vertex, 65)
	if err := ValidatePolygon(big); err == nil {
		t.Fatal("65 vertices accepted")
	}
	if err := ValidatePolygon([]Vertex{{86, 0}, {0, 1}, {0, -1}}); err == nil {
		t.Fatal("vertex past 85 latitude accepted")
	}
	if err := ValidatePolygon([]Vertex{{0, 179}, {1, -179}, {0, 178}}); err == nil {
		t.Fatal("antimeridian-crossing polygon accepted")
	}
	if err := ValidatePolygon([]Vertex{{0, 0}, {math.NaN(), 1}, {1, 1}}); err == nil {
		t.Fatal("NaN vertex accepted")
	}
	if err := ValidatePolygon([]Vertex{{0, 0}, {math.Inf(1), 1}, {1, 1}}); err == nil {
		t.Fatal("infinite vertex accepted")
	}
}

func TestPolygonFrame(t *testing.T) {
	lat, lon, r := PolygonFrame(testSquare())
	if math.Abs(lat-12.9) > 1e-9 || math.Abs(lon-77.6) > 1e-9 {
		t.Fatalf("frame centre %v,%v, want 12.9,77.6", lat, lon)
	}
	// half the square's diagonal: about 155 m; the frame radius must reach every vertex
	if r < 140 || r > 170 {
		t.Fatalf("frame radius %v m, want about 155", r)
	}
}
