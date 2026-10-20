package report

import (
	"strings"
	"testing"
	"time"
)

// A section flagged page_break starts a new PDF page; unflagged sections flow on.
func TestSectionPageBreakPDF(t *testing.T) {
	m := Metric{DeviceID: "d", PointID: "p"}
	rows := cb(10, func(i int) float64 { return float64(i) })
	series := map[Metric][]Bucket{m: rows}
	pages := func(s string) int { return strings.Count(s, "/Type /Page /Parent") }
	mk := func(brk bool) string {
		d := Definition{
			Metrics: []Metric{m}, WindowHours: 24, GroupBy: "hour",
			Sections: []Section{{Title: "Notes", Body: "first", PageBreak: brk}},
		}
		if err := Validate(d); err != nil {
			t.Fatal(err)
		}
		return string(RenderPDF("T", d, series, time.Now()))
	}
	plain, brk := mk(false), mk(true)
	if pages(brk) != pages(plain)+1 {
		t.Fatalf("page break must add exactly one page: plain=%d break=%d", pages(plain), pages(brk))
	}
	// Two flagged sections each start a new page (+2 over the same report unflagged);
	// the flag is per section, so a break before one section never splits its own body.
	two := Definition{
		Metrics: []Metric{m}, WindowHours: 24, GroupBy: "hour",
		Sections: []Section{{Title: "N1", Body: "first", PageBreak: true}, {Title: "N2", Body: "second", PageBreak: true}},
	}
	if err := Validate(two); err != nil {
		t.Fatal(err)
	}
	if p := pages(string(RenderPDF("T", two, series, time.Now()))); p != pages(plain)+2 {
		t.Fatalf("two flagged sections must add two pages: got %d want %d", p, pages(plain)+2)
	}
}

// The HTML marks a flagged section with a printed-page break; unflagged sections stay plain.
func TestSectionPageBreakHTML(t *testing.T) {
	m := Metric{DeviceID: "d", PointID: "p"}
	series := map[Metric][]Bucket{m: cb(5, func(i int) float64 { return 1 })}
	d := Definition{
		Metrics: []Metric{m}, WindowHours: 24, GroupBy: "hour",
		Sections: []Section{{Title: "A", Body: "x"}, {Title: "B", Body: "y", PageBreak: true}},
	}
	if err := Validate(d); err != nil {
		t.Fatal(err)
	}
	h := Render("T", d, series, time.Now())
	if !strings.Contains(h, `<h2 style="page-break-before:always;break-before:page">B</h2>`) {
		t.Fatal("flagged section missing the printed-page break")
	}
	if !strings.Contains(h, `<h2>A</h2>`) {
		t.Fatal("unflagged section must keep a plain heading")
	}
}

// page_break round-trips through validation unchanged and needs no extra rules.
func TestSectionPageBreakValidate(t *testing.T) {
	d := Definition{
		Metrics: []Metric{{DeviceID: "d", PointID: "p"}}, WindowHours: 24, GroupBy: "hour",
		Sections: []Section{{Title: "T", Body: "b", PageBreak: true}},
	}
	if err := Validate(d); err != nil {
		t.Fatalf("flagged section rejected: %v", err)
	}
}

// Boundary: the data block ends exactly at the page bottom (18 + 34 = perPage 52 lines on
// A4 portrait) when a flagged section arrives. The break must not insert a blank page: the
// flagged report has exactly as many pages as the unflagged one, one more than the same
// report without the section at all.
func TestSectionPageBreakExactFullPage(t *testing.T) {
	m := Metric{DeviceID: "d", PointID: "p"}
	rows := cb(34, func(i int) float64 { return float64(i) })
	series := map[Metric][]Bucket{m: rows}
	pages := func(s string) int { return strings.Count(s, "/Type /Page /Parent") }
	render := func(secs []Section) int {
		d := Definition{Metrics: []Metric{m}, WindowHours: 24, GroupBy: "hour", Sections: secs}
		if err := Validate(d); err != nil {
			t.Fatal(err)
		}
		return pages(string(RenderPDF("T", d, series, time.Now())))
	}
	plain := render([]Section{{Title: "N", Body: "x"}})
	flagged := render([]Section{{Title: "N", Body: "x", PageBreak: true}})
	if flagged != plain {
		t.Fatalf("page break at an exactly full page must not add a blank page: flagged=%d plain=%d", flagged, plain)
	}
}
