package report

import (
	"strings"
	"testing"
	"time"
)

func series(vals ...float64) []Bucket {
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var out []Bucket
	for i, v := range vals {
		out = append(out, Bucket{Start: t0.Add(time.Duration(i) * time.Hour), Avg: v, Min: v, Max: v, Sum: v, Count: 1})
	}
	return out
}

func TestInsightsTrendPeakOutlier(t *testing.T) {
	m := Metric{DeviceID: "d", PointID: "p"}
	d := Definition{Metrics: []Metric{m}, Insights: true}
	rising := Insights(d, map[Metric][]Bucket{m: series(10, 11, 12, 13, 14, 15)})[0]
	if rising.Trend != "rising" || rising.TrendPct < 30 || rising.TrendPct > 50 {
		t.Fatalf("rising %+v", rising)
	}
	flat := Insights(d, map[Metric][]Bucket{m: series(10, 10.1, 9.9, 10, 10.05, 9.95)})[0]
	if flat.Trend != "flat" {
		t.Fatalf("flat %+v", flat)
	}
	falling := Insights(d, map[Metric][]Bucket{m: series(20, 18, 16, 14)})[0]
	if falling.Trend != "falling" || falling.TrendPct >= 0 {
		t.Fatalf("falling %+v", falling)
	}
	spike := Insights(d, map[Metric][]Bucket{m: series(10, 10.2, 9.8, 10.1, 9.9, 10, 60, 10.1)})[0]
	if spike.UnusualN != 1 || spike.Unusual[0].Avg != 60 || spike.PeakValue != 60 {
		t.Fatalf("spike %+v", spike)
	}
	if two := Insights(d, map[Metric][]Bucket{m: series(1, 9)})[0]; two.Trend != "" || two.UnusualN != 0 {
		t.Fatalf("two buckets must give no trend or outliers: %+v", two)
	}
	if len(Insights(d, map[Metric][]Bucket{})) != 0 {
		t.Fatal("no data, no insight")
	}
}

func TestInsightsCompareAndRender(t *testing.T) {
	m := Metric{DeviceID: "d", PointID: "p"}
	d := Definition{Metrics: []Metric{m}, WindowHours: 6, GroupBy: "hour", Insights: true, Compare: true,
		Previous: map[Metric][]Bucket{m: series(8, 8, 8, 8)}}
	if err := Validate(d); err != nil {
		t.Fatal(err)
	}
	in := Insights(d, map[Metric][]Bucket{m: series(10, 10, 10, 10)})[0]
	if !in.HasPrev || in.ChangePct < 24.9 || in.ChangePct > 25.1 {
		t.Fatalf("compare %+v", in)
	}
	h := Render("T", d, map[Metric][]Bucket{m: series(10, 10, 10, 10)}, time.Now())
	if !strings.Contains(h, "Insights") || !strings.Contains(h, "+25.0% (was 8)") || !strings.Contains(h, "predicts nothing") {
		t.Fatal(h)
	}
	if pdf := RenderPDF("T", d, map[Metric][]Bucket{m: series(10, 10, 10, 10)}, time.Now()); !strings.Contains(string(pdf), "Insights") {
		t.Fatal("pdf has no insights")
	}
	bad := d
	bad.Insights = false
	if Validate(bad) == nil {
		t.Fatal("compare without insights accepted")
	}
	bad = d
	bad.WindowHours = 24 * 60
	if Validate(bad) == nil {
		t.Fatal("compare over 45 days accepted")
	}
	off := Definition{Metrics: []Metric{m}, WindowHours: 6, GroupBy: "hour"}
	if strings.Contains(Render("T", off, map[Metric][]Bucket{m: series(1, 2, 3)}, time.Now()), "Insights") {
		t.Fatal("insights shown when off")
	}
}
