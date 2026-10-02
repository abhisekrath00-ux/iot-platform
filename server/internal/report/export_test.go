package report

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
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
