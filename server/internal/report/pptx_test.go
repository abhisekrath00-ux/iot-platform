package report

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRenderPPTX(t *testing.T) {
	d, s := rollupFixture()
	hi := 6.0
	d.Highlight = &Highlight{Above: &hi}
	b, err := RenderPPTX("Deck <x> & \x00 title", d, s, time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	p := docxParts(t, b) // zip + every part well-formed XML
	for _, n := range []string{"[Content_Types].xml", "_rels/.rels", "ppt/presentation.xml", "ppt/_rels/presentation.xml.rels",
		"ppt/slideMasters/slideMaster1.xml", "ppt/slideLayouts/slideLayout1.xml", "ppt/theme/theme1.xml", "ppt/slides/slide1.xml", "ppt/slides/slide2.xml"} {
		if p[n] == "" {
			t.Errorf("missing part %s", n)
		}
	}
	if !strings.Contains(p["ppt/slides/slide1.xml"], "Deck &lt;x&gt; &amp;") || strings.Contains(p["ppt/slides/slide1.xml"], "<x>") {
		t.Error("title not escaped")
	}
	all := ""
	for n, v := range p {
		if strings.HasPrefix(n, "ppt/slides/slide") && strings.HasSuffix(n, ".xml") {
			all += v
		}
		if strings.Contains(v, `TargetMode="External"`) || strings.Contains(n, "vbaProject") {
			t.Errorf("%s has external content", n)
		}
	}
	if !strings.Contains(all, "m1 / kwh") || !strings.Contains(all, "Summary by asset") || !strings.Contains(all, "FECACA") {
		t.Error("tables, rollup or highlight missing")
	}
	// every slide listed in presentation.xml has a part and a content type
	for i := 1; strings.Contains(p["ppt/presentation.xml"], `r:id="rId`+itoa(9+i)+`"`); i++ {
		if p["ppt/slides/slide"+itoa(i)+".xml"] == "" || !strings.Contains(p["[Content_Types].xml"], "/ppt/slides/slide"+itoa(i)+".xml") {
			t.Errorf("slide %d not wired", i)
		}
	}
	// paging: 30 buckets -> 3 slides of at most 12 rows for one metric
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var bs []Bucket
	for i := 0; i < 30; i++ {
		bs = append(bs, Bucket{Start: t0.Add(time.Duration(i) * time.Hour), Avg: 1, Min: 1, Max: 1, Sum: 1, Count: 1})
	}
	m := Metric{"x", "y"}
	b, err = RenderPPTX("p", Definition{Metrics: []Metric{m}, WindowHours: 30, GroupBy: "hour"}, map[Metric][]Bucket{m: bs}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n := countSlides(docxParts(t, b)); n != 4 { // title + 3 pages
		t.Errorf("30 rows gave %d slides, want 4", n)
	}
	// the slide cap truncates with a note instead of growing without bound
	var ms []Metric
	series := map[Metric][]Bucket{}
	for i := 0; i < 200; i++ {
		mm := Metric{"dev" + itoa(i), "p"}
		ms = append(ms, mm)
		series[mm] = bs[:1]
	}
	b, err = RenderPPTX("big", Definition{Metrics: ms, WindowHours: 1, GroupBy: "hour"}, series, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	pp := docxParts(t, b)
	if n := countSlides(pp); n != pptxMaxSlides || !strings.Contains(pp["ppt/slides/slide"+itoa(pptxMaxSlides)+".xml"], "Report truncated") {
		t.Errorf("cap: %d slides, last=%q", n, pp["ppt/slides/slide60.xml"][:0])
	}
	if _, err = RenderPPTX("e", Definition{Metrics: d.Metrics, WindowHours: 1, GroupBy: "hour"}, map[Metric][]Bucket{}, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func countSlides(p map[string]string) int {
	n := 0
	for k := range p {
		if strings.HasPrefix(k, "ppt/slides/slide") && strings.HasSuffix(k, ".xml") {
			n++
		}
	}
	return n
}
