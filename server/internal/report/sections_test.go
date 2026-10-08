package report

import (
	"strings"
	"testing"
	"time"
)

func secDef() (Definition, map[Metric][]Bucket) {
	d, s := chartSeries()
	d.Sections = []Section{
		{Title: "Method <b>", Body: "Readings are averaged per hour.\n\nSecond line & \"quote\"."},
		{Title: "Caveat", Body: "=HYPERLINK(\"http://x\")"},
	}
	return d, s
}

func TestSectionValidation(t *testing.T) {
	d, _ := secDef()
	if err := Validate(d); err != nil {
		t.Fatal(err)
	}
	bad := map[string][]Section{
		"empty title": {{Title: " ", Body: "x"}},
		"empty body":  {{Title: "t", Body: " "}},
		"newline":     {{Title: "a\nb", Body: "x"}},
		"long title":  {{Title: strings.Repeat("a", 81), Body: "x"}},
		"long body":   {{Title: "t", Body: strings.Repeat("a", 2001)}},
		"control":     {{Title: "t", Body: "a\x01b"}},
		"too many":    make([]Section, 11),
	}
	for name, ss := range bad {
		d.Sections = ss
		if Validate(d) == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestSectionsInEveryFormat(t *testing.T) {
	d, s := secDef()
	gen := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	h := Render("T", d, s, gen)
	if !strings.Contains(h, "<h2>Method &lt;b&gt;</h2>") || !strings.Contains(h, "Second line &amp; &#34;quote&#34;.") || strings.Contains(h, "<b>") && strings.Contains(h, "Method <b>") {
		t.Fatal("html not escaped or missing")
	}
	for _, layout := range []string{"", "matrix"} {
		d.Layout = layout
		c := RenderCSV(d, s)
		if !strings.Contains(c, `"Method <b>"`) || !strings.Contains(c, `"Readings are averaged per hour."`) || strings.Contains(c, `"=HYPERLINK`) {
			t.Fatalf("csv %q: %s", layout, c)
		}
		x, err := RenderXLSX(d, s)
		if err != nil || !strings.Contains(zipText(t, x), "Readings are averaged per hour.") {
			t.Fatalf("xlsx %q: %v", layout, err)
		}
		w, _ := RenderDOCX("T", d, s, gen)
		if !strings.Contains(zipText(t, w), "Second line") {
			t.Fatalf("docx %q", layout)
		}
		p, _ := RenderPPTX("T", d, s, gen)
		if !strings.Contains(zipText(t, p), "Readings are averaged per hour.") {
			t.Fatalf("pptx %q", layout)
		}
		if pdf := RenderPDF("T", d, s, gen); len(pdf) < 500 || string(pdf[:4]) != "%PDF" {
			t.Fatalf("pdf %q", layout)
		}
	}
	d.Sections = nil
	if strings.Contains(Render("T", d, s, gen), "Method") {
		t.Fatal("no sections, no output")
	}
}
