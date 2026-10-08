package report

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"time"
)

func docxParts(t *testing.T, b []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		raw, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(raw)
		dec := xml.NewDecoder(bytes.NewReader(raw))
		for {
			if _, err := dec.Token(); err != nil {
				if err != io.EOF {
					t.Fatalf("%s is not well-formed XML: %v", f.Name, err)
				}
				break
			}
		}
	}
	return out
}

func TestRenderDOCX(t *testing.T) {
	d, s := rollupFixture()
	d.Header, d.Footer, d.Page = "Plant <A> & Co", "conf", "a4-landscape"
	hi := 6.0
	d.Highlight = &Highlight{Above: &hi}
	b, err := RenderDOCX("Weekly <b>\x00 report & more", d, s, time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	p := docxParts(t, b)
	for _, n := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml", "word/styles.xml", "word/header1.xml", "word/footer1.xml", "word/_rels/document.xml.rels"} {
		if p[n] == "" {
			t.Errorf("missing part %s", n)
		}
	}
	doc := p["word/document.xml"]
	if strings.Contains(doc, "<b>") || !strings.Contains(doc, "Weekly &lt;b&gt;") || !strings.Contains(doc, "&amp; more") {
		t.Error("title not escaped")
	}
	if !strings.Contains(doc, `w:orient="landscape"`) || !strings.Contains(doc, `w:w="16840"`) {
		t.Error("landscape A4 not applied")
	}
	if !strings.Contains(doc, "FECACA") {
		t.Error("highlight shading missing")
	}
	if !strings.Contains(doc, "m1 / kwh") || !strings.Contains(doc, "Summary by asset") || !strings.Contains(doc, "Line A") {
		t.Error("tables missing")
	}
	if !strings.Contains(p["word/header1.xml"], "Plant &lt;A&gt; &amp; Co") {
		t.Error("header not escaped")
	}
	// no external relationships or macros
	for n, v := range p {
		if strings.Contains(v, "TargetMode=\"External\"") || strings.Contains(n, "vbaProject") {
			t.Errorf("%s has external content", n)
		}
	}
	// default page is portrait A4, matrix layout and empty series both produce valid files
	d.Page, d.Layout = "", "matrix"
	d.Computed = nil
	b, err = RenderDOCX("m", d, s, time.Now())
	if err != nil || !strings.Contains(docxParts(t, b)["word/document.xml"], `w:w="11900"`) {
		t.Fatalf("matrix/portrait: %v", err)
	}
	if _, err = RenderDOCX("empty", Definition{Metrics: d.Metrics, WindowHours: 1, GroupBy: "hour"}, map[Metric][]Bucket{}, time.Now()); err != nil {
		t.Fatal(err)
	}
}
