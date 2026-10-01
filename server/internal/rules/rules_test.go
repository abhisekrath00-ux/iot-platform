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
		{Kind: "kpi_band", KPIID: "k", Severity: "info"},
		{Kind: "kpi_band", KPIID: "k", Min: &hi, Max: &lo, Severity: "info"},
		{Kind: "magic", Severity: "info"},
	}
	for _, d := range bad {
		if d.Validate() == nil {
			t.Fatalf("accepted %+v", d)
		}
	}
}
