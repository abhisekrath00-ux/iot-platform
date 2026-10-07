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
