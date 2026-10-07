package report

import (
	"math"
	"strings"
	"testing"
	"time"
)

func cb(n int, f func(i int) float64) []Bucket {
	var out []Bucket
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		out = append(out, Bucket{Start: t0.Add(time.Duration(i) * time.Hour), Avg: f(i), Count: 1})
	}
	return out
}

func TestChartKinds(t *testing.T) {
	rows := cb(6, func(i int) float64 { return float64(i * i) })
	for _, k := range []string{"line", "area", "bar"} {
		s := chartSVG(k, rows, false)
		if !strings.HasPrefix(s, "<svg") || !strings.Contains(s, "</svg>") {
			t.Fatalf("%s: %s", k, s)
		}
	}
	if !strings.Contains(chartSVG("bar", rows, true), "<rect") || !strings.Contains(chartSVG("line", rows, false), "<polyline") || !strings.Contains(chartSVG("area", rows, false), "<polygon") {
		t.Fatal("wrong primitives")
	}
	if strings.Contains(chartSVG("line", rows, false), "NaN") {
		t.Fatal("NaN")
	}
}

func TestChartEdgeCases(t *testing.T) {
	if !strings.Contains(chartSVG("line", nil, false), "not enough data") || !strings.Contains(chartSVG("line", cb(1, func(int) float64 { return 1 }), false), "not enough data") {
		t.Fatal("empty/single")
	}
	bad := cb(5, func(i int) float64 {
		if i == 2 {
			return math.NaN()
		}
		if i == 3 {
			return math.Inf(1)
		}
		return float64(i)
	})
	s := chartSVG("line", bad, false)
	if strings.Contains(s, "NaN") || strings.Contains(s, "Inf") {
		t.Fatal(s)
	}
	flat := chartSVG("bar", cb(4, func(int) float64 { return 5 }), false)
	if strings.Contains(flat, "NaN") || strings.Contains(flat, "Inf") {
		t.Fatal("flat series")
	}
	neg := chartSVG("bar", cb(4, func(i int) float64 { return float64(-i) }), false)
	if strings.Contains(neg, "NaN") || strings.Contains(neg, `height="-`) {
		t.Fatal("negative bars")
	}
	big := chartSVG("line", cb(5000, func(i int) float64 { return float64(i % 7) }), false)
	if n := strings.Count(strings.SplitN(strings.SplitN(big, `<polyline points="`, 2)[1], `"`, 2)[0], ","); n != maxChartPoints {
		t.Fatalf("points %d", n)
	}
}

func TestChartInReportAndValidation(t *testing.T) {
	d := Definition{Metrics: []Metric{{"dev-1", "temp"}}, WindowHours: 6, GroupBy: "hour", Chart: "area"}
	if err := Validate(d); err != nil {
		t.Fatal(err)
	}
	m := Metric{"dev-1", "temp"}
	series := map[Metric][]Bucket{m: cb(5, func(i int) float64 { return float64(i) })}
	h := Render("T", d, series, time.Now())
	if !strings.Contains(h, "<svg") {
		t.Fatal("no chart in table layout")
	}
	d.Layout = "matrix"
	if !strings.Contains(Render("T", d, series, time.Now()), "<svg") {
		t.Fatal("no chart in matrix layout")
	}
	d.Chart = ""
	if strings.Contains(Render("T", d, series, time.Now()), "<svg") {
		t.Fatal("chart appeared without opt-in")
	}
	d.Chart = "donut"
	if Validate(d) == nil {
		t.Fatal("pie must be rejected")
	}
	d.Chart = `"><script>`
	if Validate(d) == nil {
		t.Fatal("injection must be rejected")
	}
	if strings.Contains(chartSVG("line", series[m], false), "<script") {
		t.Fatal("script")
	}
	dark := Render("T", Definition{Metrics: d.Metrics, WindowHours: 6, GroupBy: "hour", Chart: "line", Theme: "dark"}, series, time.Now())
	if !strings.Contains(dark, "#60a5fa") {
		t.Fatal("dark series colour")
	}
}

func BenchmarkChartSVG10kBuckets(b *testing.B) {
	rows := cb(10000, func(i int) float64 { return float64(i % 97) })
	for i := 0; i < b.N; i++ {
		chartSVG("line", rows, false)
	}
}

func TestChartGaugePieScatter(t *testing.T) {
	rows := cb(12, func(i int) float64 { return float64(i + 1) })
	for i := range rows {
		rows[i].Sum = rows[i].Avg * 3
	}
	for _, k := range []string{"scatter", "gauge", "pie"} {
		s := chartSVG(k, rows, k == "pie")
		if !strings.HasPrefix(s, "<svg") || strings.Contains(s, "NaN") || strings.Contains(s, "Inf") {
			t.Fatalf("%s: %.200s", k, s)
		}
	}
	if strings.Count(chartSVG("scatter", rows, false), "<circle") != 12 {
		t.Fatal("scatter points")
	}
	if !strings.Contains(chartSVG("pie", rows, false), "other") { // 12 buckets fold to 7 + other
		t.Fatal("pie fold")
	}
	if !strings.Contains(chartSVG("gauge", rows, false), "12") { // latest value shown
		t.Fatal("gauge latest")
	}
	one := []Bucket{{Start: rows[0].Start, Avg: 5, Sum: 5, Count: 1}}
	if !strings.HasPrefix(chartSVG("gauge", one, false), "<svg") { // flat series must not divide by zero
		t.Fatal("flat gauge")
	}
	if !strings.Contains(chartSVG("pie", one, false), "not enough") || !strings.Contains(chartSVG("gauge", nil, false), "not enough") {
		t.Fatal("empty")
	}
	nan := []Bucket{{Avg: math.NaN(), Sum: math.NaN()}, {Avg: math.Inf(1), Sum: math.Inf(1)}}
	if strings.Contains(chartSVG("gauge", nan, false)+chartSVG("pie", nan, false), "NaN") {
		t.Fatal("nan leaked")
	}
	if !validChart("pie") || validChart("donut") {
		t.Fatal("validChart")
	}
}
