package report

import (
	"strings"
	"testing"
	"time"
)

func rollupFixture() (Definition, map[Metric][]Bucket) {
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	d := Definition{
		Metrics: []Metric{
			{"m1", "kwh"}, {"m2", "kwh"}, {"m3", "kwh"}, {"m1", "temp"}, {"m4", "kwh"},
		},
		WindowHours: 24, GroupBy: "hour", Rollup: "asset",
		GroupLabels: map[string]string{"m1": "Line A", "m2": "Line A", "m3": "Line B"}, // m4 unassigned
	}
	s := map[Metric][]Bucket{
		{"m1", "kwh"}:  {{Start: t0, Avg: 10, Min: 8, Max: 12, Sum: 20, Count: 2}},
		{"m2", "kwh"}:  {{Start: t0, Avg: 5, Min: 4, Max: 6, Sum: 10, Count: 2}},
		{"m3", "kwh"}:  {{Start: t0, Avg: 1, Min: 1, Max: 1, Sum: 1, Count: 1}},
		{"m1", "temp"}: {{Start: t0, Avg: 30, Min: 29, Max: 31, Sum: 60, Count: 2}},
		{"m4", "kwh"}:  {{Start: t0, Avg: 7, Min: 7, Max: 7, Sum: 7, Count: 1}},
	}
	return d, s
}

func TestRollupSubtotalsAndTotals(t *testing.T) {
	d, s := rollupFixture()
	rows := BuildRollup(d, s)
	want := []RollupRow{
		{"Line A", "kwh", 2, 4, 7.5, 4, 12, 30},
		{"Line A", "temp", 1, 2, 30, 29, 31, 60},
		{"Line B", "kwh", 1, 1, 1, 1, 1, 1},
		{UnassignedLabel, "kwh", 1, 1, 7, 7, 7, 7},
		{TotalLabel, "kwh", 4, 6, 38.0 / 6, 1, 12, 38},
		{TotalLabel, "temp", 1, 2, 30, 29, 31, 60},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows: %+v", len(rows), rows)
	}
	for i := range want {
		g, w := rows[i], want[i]
		if g.Group != w.Group || g.Point != w.Point || g.Devices != w.Devices || g.Samples != w.Samples ||
			g.Min != w.Min || g.Max != w.Max || g.Sum != w.Sum || g.Avg < w.Avg-1e-9 || g.Avg > w.Avg+1e-9 {
			t.Errorf("row %d: got %+v want %+v", i, g, w)
		}
	}
	// kwh and temp are never added together
	for _, r := range rows {
		if r.Point == "kwh" && r.Group == TotalLabel && r.Sum != 38 {
			t.Errorf("kwh total mixed units: %v", r.Sum)
		}
	}
}

func TestRollupOffAndEmpty(t *testing.T) {
	d, s := rollupFixture()
	d.Rollup = ""
	if BuildRollup(d, s) != nil {
		t.Fatal("rollup off must produce nothing")
	}
	d.Rollup = "site"
	if got := BuildRollup(d, map[Metric][]Bucket{}); len(got) != 0 {
		t.Fatalf("no data: %+v", got)
	}
}

func TestRollupValidate(t *testing.T) {
	d, _ := rollupFixture()
	if err := Validate(d); err != nil {
		t.Fatal(err)
	}
	d.Rollup = "floor"
	if Validate(d) != ErrBadRollup {
		t.Fatal("bad rollup must be rejected")
	}
}

func TestRollupRendersInAllFormats(t *testing.T) {
	d, s := rollupFixture()
	d.GroupLabels["m1"] = `<b>Line "A"</b>` // hostile name must be escaped / neutralised
	d.GroupLabels["m2"] = `<b>Line "A"</b>`
	h := Render("T", d, s, time.Now())
	if !strings.Contains(h, "Summary by asset") || strings.Contains(h, "<b>Line") || !strings.Contains(h, "&lt;b&gt;Line") {
		t.Fatal("html rollup missing or unescaped")
	}
	if !strings.Contains(h, TotalLabel) {
		t.Fatal("html total row missing")
	}
	c := RenderCSV(d, s)
	if !strings.Contains(c, `"asset","point","devices"`) || !strings.Contains(c, `"Total (all groups)","kwh","4","6"`) {
		t.Fatalf("csv rollup wrong:\n%s", c)
	}
	d.GroupLabels["m3"] = "=cmd()"
	if c := RenderCSV(d, s); !strings.Contains(c, `"'=cmd()"`) {
		t.Fatal("csv formula injection not neutralised in rollup group")
	}
	x, err := RenderXLSX(d, s)
	if err != nil || len(x) == 0 {
		t.Fatal(err)
	}
	p := RenderPDF("T", d, s, time.Now())
	if !strings.Contains(string(p), "Summary by asset") {
		t.Fatal("pdf rollup missing")
	}
	// matrix layout keeps working with rollup on
	d.Layout = "matrix"
	if !strings.Contains(Render("T", d, s, time.Now()), "Summary by asset") {
		t.Fatal("matrix html rollup missing")
	}
}
