package report

import (
	"strings"
	"testing"
	"time"
)

func TestHighlight(t *testing.T) {
	hi, lo := 30.0, 10.0
	d := Definition{Metrics: []Metric{{DeviceID: "d", PointID: "p"}}, WindowHours: 24, GroupBy: "hour", Highlight: &Highlight{Above: &hi, Below: &lo}}
	if err := Validate(d); err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	series := map[Metric][]Bucket{d.Metrics[0]: {{Start: t0, Avg: 35, Count: 1}, {Start: t0.Add(time.Hour), Avg: 20, Count: 1}, {Start: t0.Add(2 * time.Hour), Avg: 5, Count: 1}}}
	out := Render("T", d, series, time.Now())
	if strings.Count(out, "#fecaca") != 1 || strings.Count(out, "#fde68a") != 1 {
		t.Fatalf("expected one red and one amber cell")
	}
	d.Highlight = nil
	if strings.Contains(Render("T", d, series, time.Now()), "#fecaca") {
		t.Fatal("colour without opt-in")
	}
	d.Highlight = &Highlight{Above: &lo, Below: &hi}
	if Validate(d) == nil {
		t.Fatal("inverted thresholds accepted")
	}
}

func TestHighlightWhenExpression(t *testing.T) {
	h := &Highlight{When: "if({row.avg} > 50 and {row.max} < 100, 1, 0)"}
	cases := []struct {
		b    Bucket
		want string
	}{
		{Bucket{Avg: 60, Max: 80, Min: 1, Sum: 1, Count: 3}, "red"},
		{Bucket{Avg: 60, Max: 120}, ""},
		{Bucket{Avg: 40, Max: 80}, ""},
	}
	for i, c := range cases {
		if got := h.kind(c.b); got != c.want {
			t.Fatalf("case %d: %q want %q", i, got, c.want)
		}
	}
	h2 := &Highlight{When: "if({row.count} < 5 or not({row.min} > 0), 1, 0)", WhenColor: "amber"}
	if h2.kind(Bucket{Avg: 1, Min: 1, Count: 2}) != "amber" || h2.kind(Bucket{Avg: 1, Min: 1, Count: 9}) != "" {
		t.Fatal("amber / count rule wrong")
	}
	// the expression wins, then thresholds still apply as a fallback
	hi := 10.0
	h3 := &Highlight{When: "if({row.avg} > 100, 1, 0)", WhenColor: "amber", Above: &hi}
	if h3.kind(Bucket{Avg: 200}) != "amber" || h3.kind(Bucket{Avg: 20}) != "red" || h3.kind(Bucket{Avg: 5}) != "" {
		t.Fatal("precedence wrong")
	}
	// validation
	base := Definition{Metrics: []Metric{{DeviceID: "d", PointID: "p"}}, WindowHours: 24, GroupBy: "hour"}
	for _, bad := range []string{"if({d.p} > 1, 1, 0)", "if({row.nope} > 1, 1, 0)", "1 +", "if({row.avg} > 1, 1, 0"} {
		base.Highlight = &Highlight{When: bad}
		if Validate(base) == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	base.Highlight = &Highlight{When: "if({row.avg} > 1, 1, 0)", WhenColor: "blue"}
	if Validate(base) == nil {
		t.Fatal("accepted bad colour")
	}
	base.Highlight = &Highlight{When: "if({row.avg} > 1, 1, 0)", WhenColor: "amber"}
	if err := Validate(base); err != nil {
		t.Fatal(err)
	}
	// html and docx output use it
	d := base
	d.Highlight = &Highlight{When: "if({row.avg} > 5, 1, 0)"}
	rows := []Bucket{{Avg: 9, Min: 9, Max: 9, Sum: 9, Count: 1}, {Avg: 1, Min: 1, Max: 1, Sum: 1, Count: 1}}
	if got := d.Highlight.bucketStyle(rows[0]); got == "" || d.Highlight.bucketStyle(rows[1]) != "" || d.Highlight.fill(rows[0]) != "FECACA" {
		t.Fatalf("styles: %q", got)
	}
}

func TestDetailDrillThrough(t *testing.T) {
	lo := 10.0
	d := Definition{Metrics: []Metric{{DeviceID: "d", PointID: "p"}}, WindowHours: 24, GroupBy: "hour", Detail: 2, Highlight: &Highlight{Above: &lo}}
	if err := Validate(d); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []int{-1, 21} {
		d2 := d
		d2.Detail = bad
		if Validate(d2) == nil {
			t.Fatalf("detail %d accepted", bad)
		}
	}
	d3 := d
	d3.Highlight = nil
	if Validate(d3) == nil {
		t.Fatal("detail without a highlight accepted")
	}
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	m := d.Metrics[0]
	series := map[Metric][]Bucket{m: {{Start: t0, Avg: 35, Count: 2}, {Start: t0.Add(time.Hour), Avg: 5, Count: 1}}}
	d.Samples = map[Metric]map[int64][]Sample{m: {
		t0.Unix():                {{At: t0.Add(time.Minute), Value: 40}, {At: t0.Add(2 * time.Minute), Value: 30}},
		t0.Add(time.Hour).Unix(): {{At: t0.Add(time.Hour), Value: 5}},
	}}
	out := Render("T", d, series, time.Now())
	if strings.Count(out, "<details>") != 1 || !strings.Contains(out, "40.000") || strings.Contains(out, "5.000</td></tr></table></details>") {
		t.Fatalf("drill-through wrong: only the highlighted bucket gets a list\n%s", out)
	}
	d.Detail = 0
	if strings.Contains(Render("T", d, series, time.Now()), "<details>") {
		t.Fatal("drill-through without opt-in")
	}
}
