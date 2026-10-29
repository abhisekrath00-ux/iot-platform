package report

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func consistentPositionSeries() (Definition, map[Metric][]Bucket) {
	d, s := secDef()
	for m, rows := range s {
		for i := range rows {
			rows[i].Sum = rows[i].Avg * float64(rows[i].Count)
		}
		s[m] = rows
	}
	return d, s
}

func TestSectionPositionsValidateAndRender(t *testing.T) {
	d, s := consistentPositionSeries()
	d.Chart = ""
	d.Sections = []Section{{Title: "NOTE_AFTER", Body: "Trailing caveat"}, {Title: "NOTE_BEFORE", Body: "Method before data", Position: "before_data"}, {Title: "NOTE_BEFORE_TWO", Body: "Second intro", Position: "before_data"}}
	d.SubSummaries = []Section{{Title: "SUMMARY_LAST", Body: "Embedded summary"}}
	if err := Validate(d); err != nil {
		t.Fatal(err)
	}
	gen := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	ordered := func(format, text, data string) {
		t.Helper()
		a, b, c := strings.Index(text, "NOTE_BEFORE"), strings.Index(text, data), strings.Index(text, "NOTE_AFTER")
		if a < 0 || b < a || c < b || strings.Count(text, "NOTE_BEFORE_TWO") != 1 || strings.Index(text, "SUMMARY_LAST") < c {
			t.Fatalf("%s order: before=%d data=%d after=%d", format, a, b, c)
		}
	}
	part := func(blob []byte, name string) string {
		t.Helper()
		z, e := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
		if e != nil {
			t.Fatal(e)
		}
		for _, f := range z.File {
			if f.Name == name {
				r, _ := f.Open()
				b, _ := io.ReadAll(r)
				r.Close()
				return string(b)
			}
		}
		t.Fatalf("missing %s", name)
		return ""
	}
	for _, layout := range []string{"", "matrix"} {
		d.Layout = layout
		ordered("html", Render("T", d, s, gen), "<table>")
		marker := "device_id,point_id"
		if layout == "matrix" {
			marker = "hour,"
		}
		ordered("csv", RenderCSV(d, s), marker)
		x, e := RenderXLSX(d, s)
		if e != nil {
			t.Fatal(e)
		}
		ordered("xlsx", part(x, "xl/worksheets/sheet1.xml"), map[bool]string{true: ">hour<", false: ">device_id<"}[layout == "matrix"])
		w, e := RenderDOCX("T", d, s, gen)
		if e != nil {
			t.Fatal(e)
		}
		ordered("docx", part(w, "word/document.xml"), "<w:tbl>")
		p, e := RenderPPTX("T", d, s, gen)
		if e != nil {
			t.Fatal(e)
		}
		var slides string
		for i := 1; i <= 10; i++ {
			name := fmtSlide(i)
			z, _ := zip.NewReader(bytes.NewReader(p), int64(len(p)))
			for _, f := range z.File {
				if f.Name == name {
					slides += part(p, name)
				}
			}
		}
		ordered("pptx", slides, "<a:tbl>")
		pdf := string(RenderPDF("T", d, s, gen))
		ordered("pdf", pdf, map[bool]string{true: "overall", false: "bucket"}[layout == "matrix"])
	}
	d.Sections[1].Position = "unknown"
	if Validate(d) == nil {
		t.Fatal("accepted unknown placement")
	}
	d.Sections[1].Position = "after_data"
	if Validate(d) != nil {
		t.Fatal("explicit legacy placement refused")
	}
}
func fmtSlide(i int) string { return "ppt/slides/slide" + strconv.Itoa(i) + ".xml" }

func TestSectionPositionLegacyBytes(t *testing.T) {
	d, s := consistentPositionSeries()
	gen := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	before := []string{Render("T", d, s, gen), RenderCSV(d, s), string(RenderPDF("T", d, s, gen))}
	x, _ := RenderXLSX(d, s)
	w, _ := RenderDOCX("T", d, s, gen)
	p, _ := RenderPPTX("T", d, s, gen)
	before = append(before, string(x), string(w), string(p))
	for i := range d.Sections {
		d.Sections[i].Position = "after_data"
	}
	x, _ = RenderXLSX(d, s)
	w, _ = RenderDOCX("T", d, s, gen)
	p, _ = RenderPPTX("T", d, s, gen)
	after := []string{Render("T", d, s, gen), RenderCSV(d, s), string(RenderPDF("T", d, s, gen)), string(x), string(w), string(p)}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("explicit default changes format%d", i)
		}
	}
}

func TestSectionPositionVisualFixture(t *testing.T) {
	dir := os.Getenv("REPORT_POSITION_FIXTURE_DIR")
	if dir == "" {
		t.Skip("optional rendered artifact fixture")
	}
	d, s := consistentPositionSeries()
	d.Chart = "line"
	d.Sections = []Section{{Title: "Method before data", Body: "Synthetic readings only. Confirm the meter units before use.", Position: "before_data"}, {Title: "Coverage after data", Body: "Missing samples are not zero.", PageBreak: true}}
	g := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "position.html"), []byte(Render("Synthetic placement check", d, s, g)), 0600)
	os.WriteFile(filepath.Join(dir, "position.pdf"), RenderPDF("Synthetic placement check", d, s, g), 0600)
	x, _ := RenderXLSX(d, s)
	w, _ := RenderDOCX("Synthetic placement check", d, s, g)
	p, _ := RenderPPTX("Synthetic placement check", d, s, g)
	for name, b := range map[string][]byte{"position.xlsx": x, "position.docx": w, "position.pptx": p} {
		os.WriteFile(filepath.Join(dir, name), b, 0600)
	}
}

func TestBeforeSectionPageBreak(t *testing.T) {
	d, s := consistentPositionSeries()
	d.Sections = []Section{{Title: "Intro", Body: "Before data", Position: "before_data"}}
	gen := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	plain := string(RenderPDF("T", d, s, gen))
	d.Sections[0].PageBreak = true
	brk := string(RenderPDF("T", d, s, gen))
	if strings.Count(brk, "/Type /Page /Parent") != strings.Count(plain, "/Type /Page /Parent")+1 {
		t.Fatal("intro break did not add page")
	}
	if !strings.Contains(Render("T", d, s, gen), `<h2 style="page-break-before:always;break-before:page">Intro`) {
		t.Fatal("intro print break missing")
	}
}
