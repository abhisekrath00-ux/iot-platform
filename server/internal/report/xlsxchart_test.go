package report

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"image"
	"io"
	"strings"
	"testing"
)

func xlsxParts(t *testing.T, b []byte) map[string]string {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		r, _ := f.Open()
		body, _ := io.ReadAll(r)
		r.Close()
		out[f.Name] = string(body)
		if strings.HasSuffix(f.Name, ".xml") || strings.HasSuffix(f.Name, ".rels") {
			d := xml.NewDecoder(bytes.NewReader(body))
			for {
				if _, err := d.Token(); err != nil {
					if err != io.EOF {
						t.Fatalf("%s not well formed: %v", f.Name, err)
					}
					break
				}
			}
		}
	}
	return out
}

func TestXLSXNativeCharts(t *testing.T) {
	m1, m2 := Metric{DeviceID: "a", PointID: "t"}, Metric{DeviceID: "a", PointID: "h"}
	rows := cb(10, func(i int) float64 { return float64(i) })
	series := map[Metric][]Bucket{m1: rows, m2: rows[:1]} // m2 has one bucket: no chart
	for _, k := range []string{"line", "area", "bar", "scatter"} {
		d := Definition{Metrics: []Metric{m1, m2}, WindowHours: 24, GroupBy: "hour", Chart: k}
		b, err := RenderXLSX(d, series)
		if err != nil {
			t.Fatal(err)
		}
		p := xlsxParts(t, b)
		if _, ok := p["xl/charts/chart1.xml"]; !ok {
			t.Fatalf("%s: no chart part", k)
		}
		if _, ok := p["xl/charts/chart2.xml"]; ok {
			t.Fatalf("%s: chart for a one-bucket metric", k)
		}
		if !strings.Contains(p["xl/charts/chart1.xml"], "Report!$D$2:$D$11") || !strings.Contains(p["xl/worksheets/sheet1.xml"], `<drawing r:id="rId1"/>`) || !strings.Contains(p["[Content_Types].xml"], "chart1.xml") {
			t.Fatalf("%s: wiring wrong: %.300s | %.200s", k, p["xl/charts/chart1.xml"], p["xl/worksheets/sheet1.xml"][len(p["xl/worksheets/sheet1.xml"])-120:])
		}
	}
	for _, k := range []string{"", "pie", "gauge"} { // no native equivalent or not requested
		b, _ := RenderXLSX(Definition{Metrics: []Metric{m1}, WindowHours: 24, GroupBy: "hour", Chart: k}, series)
		p := xlsxParts(t, b)
		if _, ok := p["xl/charts/chart1.xml"]; ok || strings.Contains(p["xl/worksheets/sheet1.xml"], "<drawing") {
			t.Fatalf("%q: unexpected chart", k)
		}
	}
	// matrix layout keeps no charts; logo and chart share one drawing
	b, _ := RenderXLSX(Definition{Metrics: []Metric{m1}, WindowHours: 24, GroupBy: "hour", Chart: "line", Layout: "matrix"}, series)
	if _, ok := xlsxParts(t, b)["xl/charts/chart1.xml"]; ok {
		t.Fatal("matrix layout got a chart")
	}
	d := Definition{Metrics: []Metric{m1}, WindowHours: 24, GroupBy: "hour", Chart: "line", Logo: image.NewRGBA(image.Rect(0, 0, 40, 20))}
	b, _ = RenderXLSX(d, series)
	p := xlsxParts(t, b)
	if !strings.Contains(p["xl/drawings/drawing1.xml"], "xdr:pic") || !strings.Contains(p["xl/drawings/drawing1.xml"], "graphicFrame") || p["xl/media/logo.png"] == "" {
		t.Fatal("logo and chart must share the drawing")
	}
	// the metric cap
	var ms []Metric
	big := map[Metric][]Bucket{}
	for i := 0; i < 12; i++ {
		m := Metric{DeviceID: "d", PointID: "p" + string(rune('a'+i))}
		ms = append(ms, m)
		big[m] = rows
	}
	b, _ = RenderXLSX(Definition{Metrics: ms, WindowHours: 24, GroupBy: "hour", Chart: "bar"}, big)
	p = xlsxParts(t, b)
	if _, ok := p["xl/charts/chart8.xml"]; !ok {
		t.Fatal("expected 8 charts")
	}
	if _, ok := p["xl/charts/chart9.xml"]; ok {
		t.Fatal("chart cap exceeded")
	}
}
