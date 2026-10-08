package report

import (
	"archive/zip"
	"bytes"
	"image/png"
	"io"
	"strings"
	"testing"
	"time"
)

func chartSeries() (Definition, map[Metric][]Bucket) {
	m := Metric{"d1", "temp"}
	t0 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	var bs []Bucket
	for i := 0; i < 12; i++ {
		bs = append(bs, Bucket{Start: t0.Add(time.Duration(i) * time.Hour), Avg: float64(10 + i*i%7), Min: 1, Max: 20, Sum: 100, Count: 4})
	}
	return Definition{Metrics: []Metric{m}, WindowHours: 12, GroupBy: "hour"}, map[Metric][]Bucket{m: bs}
}

func TestChartPNGKindsAndCaption(t *testing.T) {
	d, s := chartSeries()
	rows := s[d.Metrics[0]]
	for _, k := range []string{"line", "area", "bar", "scatter"} {
		b, cap := ChartPNG(k, rows)
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil || img.Bounds().Dx() != pngW {
			t.Fatalf("%s: %v", k, err)
		}
		if !strings.Contains(cap, "lowest 10.000") || !strings.Contains(cap, "12 buckets") {
			t.Fatalf("%s caption %q", k, cap)
		}
	}
	for _, k := range []string{"", "gauge", "pie"} {
		if b, _ := ChartPNG(k, rows); b != nil {
			t.Errorf("%q should draw nothing", k)
		}
	}
	if b, _ := ChartPNG("line", rows[:1]); b != nil {
		t.Error("one bucket must not chart")
	}
	flat := []Bucket{{Avg: 5, Count: 1}, {Avg: 5, Count: 1}}
	if b, _ := ChartPNG("bar", flat); b == nil {
		t.Error("flat series should still draw")
	}
}

func zipFiles(t *testing.T, b []byte) map[string]bool {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]bool{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		io.Copy(io.Discard, rc)
		rc.Close()
		m[f.Name] = true
	}
	return m
}

func TestWordAndPowerPointCarryCharts(t *testing.T) {
	d, s := chartSeries()
	gen := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	for _, layout := range []string{"", "matrix"} {
		d.Layout = layout
		d.Chart = "line"
		w, err := RenderDOCX("T", d, s, gen)
		if err != nil {
			t.Fatal(err)
		}
		if f := zipFiles(t, w); !f["word/media/chart1.png"] {
			t.Errorf("docx %q: no chart", layout)
		}
		if !strings.Contains(zipText(t, w), "Average per bucket") {
			t.Errorf("docx %q: no caption", layout)
		}
		p, err := RenderPPTX("T", d, s, gen)
		if err != nil {
			t.Fatal(err)
		}
		if f := zipFiles(t, p); !f["ppt/media/chart1.png"] {
			t.Errorf("pptx %q: no chart", layout)
		}
		d.Chart = ""
		w, _ = RenderDOCX("T", d, s, gen)
		p, _ = RenderPPTX("T", d, s, gen)
		for n := range zipFiles(t, w) {
			if strings.Contains(n, "media") {
				t.Errorf("docx without chart has %s", n)
			}
		}
		for n := range zipFiles(t, p) {
			if strings.Contains(n, "media") {
				t.Errorf("pptx without chart has %s", n)
			}
		}
	}
}
