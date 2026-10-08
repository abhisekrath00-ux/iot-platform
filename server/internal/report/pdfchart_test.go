package report

import (
	"bytes"
	"math"
	"os"
	"testing"
	"time"
)

func pdfSeries() (Definition, map[Metric][]Bucket) {
	m := Metric{DeviceID: "boiler-1", PointID: "temp"}
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var bs []Bucket
	for i := 0; i < 24; i++ {
		v := 50 + 20*math.Sin(float64(i)/4)
		bs = append(bs, Bucket{Start: t0.Add(time.Duration(i) * time.Hour), Avg: v, Min: v - 3, Max: v + 3, Sum: v * 3, Count: 3})
	}
	return Definition{Metrics: []Metric{m}, WindowHours: 24, GroupBy: "hour"}, map[Metric][]Bucket{m: bs}
}

func TestPDFChartKinds(t *testing.T) {
	d, series := pdfSeries()
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	base := RenderPDF("T", d, series, now)
	seen := map[string]bool{string(base): true}
	for _, k := range []string{"line", "bar", "area", "scatter", "gauge", "pie"} {
		d.Chart = k
		out := RenderPDF("T", d, series, now)
		if !bytes.HasPrefix(out, []byte("%PDF-1.4")) {
			t.Fatalf("%s: not a pdf", k)
		}
		if k == "line" && !bytes.Equal(out, base) {
			t.Fatal("explicit line must equal the default PDF")
		}
		if k != "line" {
			if seen[string(out)] {
				t.Fatalf("%s: same bytes as another chart type", k)
			}
			seen[string(out)] = true
		}
		if dir := os.Getenv("PDF_CHART_OUT"); dir != "" {
			os.WriteFile(dir+"/"+k+".pdf", out, 0o644)
		}
	}
	// empty and single-bucket series must not panic
	for _, k := range []string{"bar", "area", "scatter", "gauge", "pie"} {
		d.Chart = k
		one := map[Metric][]Bucket{d.Metrics[0]: series[d.Metrics[0]][:1]}
		RenderPDF("T", d, one, now)
		RenderPDF("T", d, map[Metric][]Bucket{d.Metrics[0]: {{Start: now, Avg: math.NaN(), Sum: math.Inf(1), Count: 0}}}, now)
	}
}
