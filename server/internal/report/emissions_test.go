package report

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

// The factors below are made-up test numbers, not real emission factors.
func emDef() (Definition, map[Metric][]Bucket) {
	kwh, diesel, units := Metric{"meter-1", "kwh"}, Metric{"tank-1", "diesel_l"}, Metric{"line-a", "units"}
	t0 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	d := Definition{Metrics: []Metric{kwh, diesel, units}, WindowHours: 48, GroupBy: "day",
		Emissions: []EmissionSource{
			{Name: "Grid electricity", Scope: 2, Metric: kwh, Unit: "kWh", Factor: 0.8, FactorSource: "TEST FACTOR, not real"},
			{Name: "Diesel genset", Scope: 1, Metric: diesel, Mode: "delta", Unit: "L", Factor: 2.5, FactorSource: "TEST FACTOR 2"},
		},
		EmissionsIntensity: &EmissionsIntensity{Label: "per unit made", Metric: units}}
	s := map[Metric][]Bucket{
		kwh:    {{Start: t0, Sum: 1000, Count: 10, Avg: 100, Min: 90, Max: 110}, {Start: t0.Add(24 * time.Hour), Sum: 500, Count: 5, Avg: 100, Min: 90, Max: 110}},
		diesel: {{Start: t0, Min: 100, Max: 130, Count: 5}, {Start: t0.Add(24 * time.Hour), Min: 130, Max: 160, Count: 5}},
		units:  {{Start: t0, Sum: 450, Count: 3}},
	}
	return d, s
}

func TestEmissionsMath(t *testing.T) {
	d, s := emDef()
	if err := Validate(d); err != nil {
		t.Fatal(err)
	}
	r := BuildEmissions(d, s)
	near := func(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }
	if !near(r.Scope2, 1.2) || !near(r.Scope1, 0.15) || !near(r.Total, 1.35) {
		t.Fatalf("got s2=%v s1=%v total=%v", r.Scope2, r.Scope1, r.Total)
	}
	if r.Rows[1].Quantity != 60 {
		t.Fatalf("delta quantity %v", r.Rows[1].Quantity)
	}
	if !strings.Contains(r.Intensity, "0.003") {
		t.Fatalf("intensity %q", r.Intensity)
	}
}

func TestEmissionsGapsAreNotSilent(t *testing.T) {
	d, s := emDef()
	s[Metric{"tank-1", "diesel_l"}] = []Bucket{{Min: 200, Max: 50, Count: 2}} // counter went backwards
	delete(s, Metric{"meter-1", "kwh"})
	r := BuildEmissions(d, s)
	if r.Rows[0].Note != "no data" || !strings.Contains(r.Rows[1].Note, "backwards") || r.Total != 0 {
		t.Fatalf("%+v", r.Rows)
	}
}

func TestEmissionsValidation(t *testing.T) {
	for name, mut := range map[string]func(*Definition){
		"no factor":         func(d *Definition) { d.Emissions[0].Factor = 0 },
		"nan-ish factor":    func(d *Definition) { d.Emissions[0].Factor = -1 },
		"no source text":    func(d *Definition) { d.Emissions[0].FactorSource = " " },
		"bad scope":         func(d *Definition) { d.Emissions[0].Scope = 3 },
		"metric not in rpt": func(d *Definition) { d.Emissions[0].Metric = Metric{"x", "y"} },
		"bad mode":          func(d *Definition) { d.Emissions[0].Mode = "avg" },
		"no unit":           func(d *Definition) { d.Emissions[0].Unit = "" },
		"too many":          func(d *Definition) { d.Emissions = append(d.Emissions, make([]EmissionSource, 20)...) },
		"intensity alone":   func(d *Definition) { d.Emissions = nil },
	} {
		d, _ := emDef()
		mut(&d)
		if err := Validate(d); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func zipText(t *testing.T, b []byte) string {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for _, f := range zr.File {
		rc, _ := f.Open()
		x, _ := io.ReadAll(rc)
		rc.Close()
		sb.Write(x)
	}
	return sb.String()
}

func TestEmissionsInEveryFormat(t *testing.T) {
	d, s := emDef()
	gen := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	check := func(name, out string, want ...string) {
		for _, w := range append(want, "Scope 1 + 2 total") {
			if !strings.Contains(out, w) {
				t.Errorf("%s: missing %q", name, w)
			}
		}
	}
	notice := "not a certified or assured"
	check("html", Render("T", d, s, gen), "1.2000", "0.1500", "1.3500", "TEST FACTOR, not real", notice)
	check("csv", RenderCSV(d, s), "1.2000", "TEST FACTOR 2", "not a certified")
	x, err := RenderXLSX(d, s)
	if err != nil {
		t.Fatal(err)
	}
	check("xlsx", zipText(t, x), "Grid electricity", "TEST FACTOR, not real", notice)
	w, err := RenderDOCX("T", d, s, gen)
	if err != nil {
		t.Fatal(err)
	}
	check("docx", zipText(t, w), "1.3500", "TEST FACTOR 2", notice)
	p, err := RenderPPTX("T", d, s, gen)
	if err != nil {
		t.Fatal(err)
	}
	check("pptx", zipText(t, p), "Grid electricity", notice)
	pdf := string(RenderPDF("T", d, s, gen))
	for _, w := range []string{`Emissions \(Scope 1 and 2\)`, "Scope 1 total 0.1500", "TEST FACTOR, not real"} {
		if !strings.Contains(pdf, w) {
			t.Errorf("pdf: missing %q", w)
		}
	}
	// matrix layout still carries the section
	d.Layout = "matrix"
	if !strings.Contains(Render("T", d, s, gen), "Emissions (Scope 1 and 2)") {
		t.Error("matrix html missing section")
	}
}
