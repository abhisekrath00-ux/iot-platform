package rules

import "testing"

func TestThresholdLogic(t *testing.T) {
	cases := []struct {
		op        string
		threshold float64
		value     float64
		want      bool
	}{
		{">", 100, 101, true},
		{">", 100, 100, false},
		{">", 100, 99, false},
		{"<", 0, -1, true},
		{"<", 0, 0, false},
		{"<", 0, 1, false},
	}
	for _, c := range cases {
		hit := (c.op == ">" && c.value > c.threshold) || (c.op == "<" && c.value < c.threshold)
		if hit != c.want {
			t.Errorf("op=%s threshold=%v value=%v: got %v want %v", c.op, c.threshold, c.value, hit, c.want)
		}
	}
}

func TestSigmaDeviationAndBand(t *testing.T) {
	base := make([]float64, 40)
	for i := range base {
		base[i] = 20 + float64(i%3) // mean ~21, small spread
	}
	z, _, _, ok := SigmaDeviation(base, 40)
	if !ok || z < 10 {
		t.Fatalf("z=%v ok=%v", z, ok)
	}
	if z, _, _, ok := SigmaDeviation(base, 21); !ok || z > 1 {
		t.Fatalf("typical value z=%v", z)
	}
	if _, _, _, ok := SigmaDeviation(base[:10], 40); ok {
		t.Fatal("tiny baseline must not judge")
	}
	flat := make([]float64, 40)
	if _, _, _, ok := SigmaDeviation(flat, 5); ok {
		t.Fatal("flat baseline has no spread, must not judge")
	}
	// robust: a spiky baseline does not mask a real outlier
	spiky := make([]float64, 40)
	for i := range spiky {
		spiky[i] = 20 + float64(i%3) // 20..22 like the mean case
	}
	spiky[5], spiky[17], spiky[29] = 45, 50, 42 // three spikes inflate the std
	zr, med, scale, ok := RobustDeviation(spiky, 34)
	if !ok || zr < 3 {
		t.Fatalf("robust z=%v ok=%v (med %v scale %v)", zr, ok, med, scale)
	}
	if zm, _, _, _ := SigmaDeviation(spiky, 34); zm >= zr {
		t.Fatalf("robust (%v) should beat mean/std (%v) on a spiky baseline", zr, zm)
	}
	if z, _, _, ok := RobustDeviation(spiky, 21); !ok || z > 1 {
		t.Fatalf("robust typical value z=%v", z)
	}
	if _, _, _, ok := RobustDeviation(spiky[:10], 40); ok {
		t.Fatal("robust: tiny baseline must not judge")
	}
	if _, _, _, ok := RobustDeviation(flat, 5); ok {
		t.Fatal("robust: flat baseline must not judge")
	}
	lo, hi := 10.0, 30.0
	if side, out := BandBreach(5, &lo, &hi); !out || side != "below" {
		t.Fatal("below")
	}
	if side, out := BandBreach(31, &lo, &hi); !out || side != "above" {
		t.Fatal("above")
	}
	if _, out := BandBreach(20, &lo, &hi); out {
		t.Fatal("inside")
	}
	if _, out := BandBreach(1000, &lo, nil); out {
		t.Fatal("no max means no upper breach")
	}
}

func TestDefinitionValidate(t *testing.T) {
	lo, hi := 1.0, 2.0
	ok := []Definition{
		{PointID: "p", Op: ">", Severity: "info"},
		{Kind: "sigma", PointID: "p", Sigma: 3, WindowMinutes: 60, Severity: "warning"},
		{Kind: "kpi_band", KPIID: "k", Min: &lo, Severity: "critical"},
		{Kind: "sigma", PointID: "p", Sigma: 3, WindowMinutes: 60, Method: "robust", Severity: "info"},
		{PointID: "p", Op: ">", Severity: "info", AssetID: "a", Relation: "feeds"},
	}
	for _, d := range ok {
		if err := d.Validate(); err != nil {
			t.Fatalf("%+v: %v", d, err)
		}
	}
	bad := []Definition{
		{PointID: "p", Op: ">", Severity: "loud"},
		{Kind: "sigma", PointID: "p", Sigma: 1, WindowMinutes: 60, Severity: "info"},
		{Kind: "sigma", PointID: "p", Sigma: 3, WindowMinutes: 5, Severity: "info"},
		{Kind: "sigma", PointID: "p", Sigma: 3, WindowMinutes: 60, Direction: "up", Severity: "info"},
		{Kind: "sigma", PointID: "p", Sigma: 3, WindowMinutes: 60, Method: "bogus", Severity: "info"},
		{Kind: "threshold", PointID: "p", Op: ">", Threshold: 1, Method: "robust", Severity: "info"},
		{Kind: "kpi_band", KPIID: "k", Severity: "info"},
		{Kind: "kpi_band", KPIID: "k", Min: &hi, Max: &lo, Severity: "info"},
		{Kind: "magic", Severity: "info"},
		{PointID: "p", Op: ">", Severity: "info", Relation: "feeds"},                  // relation without asset_id
		{PointID: "p", Op: ">", Severity: "info", AssetID: "a", Relation: "Feeds"},    // upper-case
		{PointID: "p", Op: ">", Severity: "info", AssetID: "a", Relation: "1feeds"},   // leading digit
		{PointID: "p", Op: ">", Severity: "info", AssetID: "a", Relation: "has-dash"}, // bad character
	}
	for _, d := range bad {
		if d.Validate() == nil {
			t.Fatalf("accepted %+v", d)
		}
	}
}
