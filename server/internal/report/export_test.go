package report

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

func sample() (Definition, map[Metric][]Bucket) {
	m := Metric{DeviceID: "meter-1", PointID: "kw"}
	d := Definition{Metrics: []Metric{m}, WindowHours: 24, GroupBy: "hour"}
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var rows []Bucket
	for i := 0; i < 120; i++ {
		rows = append(rows, Bucket{Start: t0.Add(time.Duration(i) * time.Hour), Avg: float64(i), Min: 1, Max: 9, Sum: 10, Count: 3})
	}
	return d, map[Metric][]Bucket{m: rows}
}

func TestXLSXIsValidZipWithRowsAndEscapes(t *testing.T) {
	d, s := sample()
	d.Metrics[0].DeviceID = `a&b<"=cmd">`
	s = map[Metric][]Bucket{d.Metrics[0]: s[Metric{DeviceID: "meter-1", PointID: "kw"}]}
	b, err := RenderXLSX(d, s)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	var sheet string
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
		if f.Name == "xl/worksheets/sheet1.xml" {
			rc, _ := f.Open()
			x, _ := io.ReadAll(rc)
			sheet = string(x)
		}
	}
	for _, n := range []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/_rels/workbook.xml.rels"} {
		if !names[n] {
			t.Fatalf("missing part %s", n)
		}
	}
	if strings.Count(sheet, "<row ") != 121 {
		t.Fatalf("rows = %d", strings.Count(sheet, "<row "))
	}
	if strings.Contains(sheet, `a&b<`) || !strings.Contains(sheet, "a&amp;b&lt;") {
		t.Fatalf("device id not escaped")
	}
}

func TestPDFStructureAndPagination(t *testing.T) {
	d, s := sample()
	b := RenderPDF("Plant (daily) \\ report é", d, s, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC))
	p := string(b)
	if !strings.HasPrefix(p, "%PDF-1.4") || !strings.HasSuffix(p, "%%EOF\n") {
		t.Fatal("bad header/trailer")
	}
	pages := strings.Count(p, "/Type /Page ")
	if pages < 2 {
		t.Fatalf("expected pagination, pages=%d", pages)
	}
	if !strings.Contains(p, "Page 1 of ") || !strings.Contains(p, `\(cont.\)`) {
		t.Fatal("missing page footer or continued header")
	}
	if !strings.Contains(p, `Plant \(daily\) \\ report ?`) {
		t.Fatal("text not escaped")
	}
	// xref offsets must point at "N 0 obj"
	x := strings.LastIndex(p, "startxref\n")
	var off int
	if _, err := fmtSscan(p[x+len("startxref\n"):], &off); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p[off:], "xref\n") {
		t.Fatal("startxref does not point at xref")
	}
	lines := strings.Split(p[off:], "\n")
	for i := 3; i < 3+3; i++ {
		var o int
		o, _ = strconv.Atoi(strings.Fields(lines[i])[0])
		if !strings.Contains(p[o:o+12], " 0 obj") {
			t.Fatalf("xref entry %d offset %d not an object", i-2, o)
		}
	}
}

func TestPDFEmptyDefinition(t *testing.T) {
	b := RenderPDF("x", Definition{}, nil, time.Now())
	if !strings.Contains(string(b), "/Count 1") {
		t.Fatal("empty report must still be one page")
	}
}

func fmtSscan(s string, v *int) (int, error) { return fmt.Sscan(s, v) }

func TestMatrixLayoutAcrossFormats(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	a, b := Metric{"d1", "kw"}, Metric{"d2", "kw"}
	d := Definition{Metrics: []Metric{a, b}, WindowHours: 24, GroupBy: "hour", Layout: "matrix", Agg: "max"}
	s := map[Metric][]Bucket{
		a: {{Start: t0, Avg: 1, Min: 1, Max: 5, Sum: 2, Count: 2}, {Start: t0.Add(time.Hour), Avg: 2, Min: 2, Max: 7, Sum: 4, Count: 2}},
		b: {{Start: t0.Add(time.Hour), Avg: 3, Min: 3, Max: 9, Sum: 6, Count: 2}},
	}
	hdr, rows, total := Matrix(d, s)
	if len(hdr) != 2 || len(rows) != 2 {
		t.Fatalf("shape %v %v", hdr, rows)
	}
	if rows[0][2] != "-" || rows[0][1] != "5.000" || rows[1][2] != "9.000" {
		t.Fatalf("cells %v", rows)
	}
	if total[1] != "7.000" || total[2] != "9.000" {
		t.Fatalf("totals %v", total)
	}
	h := Render("t", d, s, t0)
	if strings.Count(h, "<table>") != 1 || !strings.Contains(h, "d1 / kw") {
		t.Fatal("html matrix")
	}
	if p := string(RenderPDF("t", d, s, t0)); !strings.Contains(p, "d2 / kw") || !strings.Contains(p, "overall") {
		t.Fatal("pdf matrix")
	}
	x, err := RenderXLSX(d, s)
	if err != nil {
		t.Fatal(err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(x), int64(len(x)))
	var sheet string
	for _, f := range zr.File {
		if f.Name == "xl/worksheets/sheet1.xml" {
			rc, _ := f.Open()
			bb, _ := io.ReadAll(rc)
			sheet = string(bb)
		}
	}
	if strings.Count(sheet, "<row ") != 4 || !strings.Contains(sheet, "<v>9</v>") {
		t.Fatalf("xlsx matrix: %s", sheet)
	}
	d.Layout, d.Agg = "pivot", ""
	if Validate(Definition{Metrics: d.Metrics, WindowHours: 1, GroupBy: "hour", Layout: "pivot"}) == nil {
		t.Fatal("bad layout accepted")
	}
	if Validate(Definition{Metrics: d.Metrics, WindowHours: 1, GroupBy: "hour", Agg: "median"}) == nil {
		t.Fatal("bad agg accepted")
	}
}

func TestApplyParamsAndCSVMatrix(t *testing.T) {
	d := Definition{Metrics: []Metric{{"d1", "kw"}}, WindowHours: 24, GroupBy: "hour"}
	q := map[string]string{"window_hours": "48", "group_by": "day", "layout": "matrix", "agg": "sum"}
	got, err := ApplyParams(d, func(k string) string { return q[k] })
	if err != nil || got.WindowHours != 48 || got.GroupBy != "day" || got.Layout != "matrix" || got.Agg != "sum" {
		t.Fatalf("%+v %v", got, err)
	}
	if d.WindowHours != 24 {
		t.Fatal("stored definition mutated")
	}
	for _, bad := range []map[string]string{{"window_hours": "0"}, {"window_hours": "x"}, {"group_by": "year"}, {"layout": "cube"}, {"agg": "p99"}} {
		if _, err := ApplyParams(d, func(k string) string { return bad[k] }); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
	got.Layout = "matrix"
	csv := RenderCSV(got, map[Metric][]Bucket{{"d1", "kw"}: {{Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Sum: 4, Count: 1}}})
	if !strings.Contains(csv, `"d1 / kw"`) || !strings.Contains(csv, "4.000") {
		t.Fatalf("csv matrix: %s", csv)
	}
}

func TestComputedMatrixColumns(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	kwh, units := Metric{"meter-1", "kwh"}, Metric{"line-a", "units"}
	d := Definition{Metrics: []Metric{kwh, units}, WindowHours: 24, GroupBy: "hour", Layout: "matrix", Agg: "sum",
		Computed: []Computed{{Name: "kWh per unit", Expr: "{meter-1.kwh} / {line-a.units}"}}}
	if err := Validate(d); err != nil {
		t.Fatal(err)
	}
	s := map[Metric][]Bucket{
		kwh:   {{Start: t0, Sum: 50, Count: 1}, {Start: t0.Add(time.Hour), Sum: 30, Count: 1}},
		units: {{Start: t0, Sum: 10, Count: 1}, {Start: t0.Add(time.Hour), Sum: 0, Count: 1}},
	}
	hdr, rows, total := Matrix(d, s)
	if hdr[2] != "kWh per unit" || rows[0][3] != "5.000" {
		t.Fatalf("computed cell: %v %v", hdr, rows)
	}
	if rows[1][3] != "-" { // divide by zero is shown as missing, never as Inf
		t.Fatalf("div by zero cell = %q", rows[1][3])
	}
	if total[3] != "8.000" { // 80 kWh / 10 units
		t.Fatalf("total computed = %q", total[3])
	}
	if !strings.Contains(Render("t", d, s, t0), "kWh per unit") || !strings.Contains(RenderCSV(d, s), "kWh per unit") {
		t.Fatal("computed column missing from output")
	}
	bad := []Definition{
		{Metrics: d.Metrics, WindowHours: 1, GroupBy: "hour", Computed: []Computed{{"x", "1"}}},                                   // not matrix
		{Metrics: d.Metrics, WindowHours: 1, GroupBy: "hour", Layout: "matrix", Computed: []Computed{{"x", "{other.thing} * 2"}}}, // foreign ref
		{Metrics: d.Metrics, WindowHours: 1, GroupBy: "hour", Layout: "matrix", Computed: []Computed{{"x", "{meter-1.kwh} ^ 2"}}}, // bad grammar
		{Metrics: d.Metrics, WindowHours: 1, GroupBy: "hour", Layout: "matrix", Computed: []Computed{{"", "{meter-1.kwh}"}}},      // no name
	}
	for i, b := range bad {
		if Validate(b) == nil {
			t.Fatalf("bad definition %d accepted", i)
		}
	}
}

func TestPDFDrawsCharts(t *testing.T) {
	d, s := sample()
	p := string(RenderPDF("Plant", d, s, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)))
	// one chart per metric with data: frame rectangle plus three polylines
	if n := strings.Count(p, " re S\n"); n != len(s) {
		t.Fatalf("charts %d, metrics with data %d", n, len(s))
	}
	if !strings.Contains(p, `avg, min, max\)`) || !strings.Contains(p, "1.2 w") || !strings.Contains(p, " l\n") {
		t.Fatal("chart operators or title missing")
	}
	if strings.Contains(p, "NaN") || strings.Contains(p, "Inf") {
		t.Fatal("non-finite coordinate in content stream")
	}
	// single bucket, flat series and NaN values must not break the stream
	m := Metric{DeviceID: "d", PointID: "p"}
	one := map[Metric][]Bucket{m: {{Start: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Avg: 5, Min: 5, Max: 5, Count: 1}}}
	q := string(RenderPDF("x", Definition{Metrics: []Metric{m}, WindowHours: 24, GroupBy: "hour"}, one, time.Now()))
	if strings.Contains(q, "NaN") || strings.Contains(q, "Inf") || !strings.Contains(q, " re S\n") {
		t.Fatal("flat single-point chart")
	}
	nan := map[Metric][]Bucket{m: {{Start: time.Now(), Avg: math.NaN(), Min: math.NaN(), Max: math.NaN()}}}
	if r := string(RenderPDF("x", Definition{Metrics: []Metric{m}, WindowHours: 24, GroupBy: "hour"}, nan, time.Now())); strings.Contains(r, " re S\n") || strings.Contains(r, "NaN m") {
		t.Fatal("all-NaN series must draw no chart")
	}
	// matrix layout charts the first four metrics only
	var ms []Metric
	big := map[Metric][]Bucket{}
	for i := 0; i < 6; i++ {
		mm := Metric{DeviceID: "d", PointID: "p" + strconv.Itoa(i)}
		ms = append(ms, mm)
		big[mm] = one[m]
	}
	r := string(RenderPDF("x", Definition{Metrics: ms, WindowHours: 24, GroupBy: "hour", Layout: "matrix"}, big, time.Now()))
	if n := strings.Count(r, " re S\n"); n != 4 || !strings.Contains(r, "first 4 of 6 metrics") {
		t.Fatalf("matrix charts %d", n)
	}
}
