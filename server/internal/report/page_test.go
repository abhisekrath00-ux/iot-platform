package report

import (
	"math"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestPageSetup(t *testing.T) {
	m := Metric{DeviceID: "d", PointID: "p"}
	rows := cb(120, func(i int) float64 { return 10 + math.Sin(float64(i)) })
	series := map[Metric][]Bucket{m: rows}
	mk := func(page string) string {
		d := Definition{Metrics: []Metric{m}, WindowHours: 24, GroupBy: "hour", Page: page, Header: "H", Footer: "F"}
		if err := Validate(d); err != nil {
			t.Fatal(err)
		}
		return string(RenderPDF("T", d, series, time.Now()))
	}
	box := regexp.MustCompile(`/MediaBox \[0 0 (\d+) (\d+)\]`)
	pages := func(s string) int { return strings.Count(s, "/Type /Page /Parent") }
	def, a4, land, letter := mk(""), mk("a4"), mk("a4-landscape"), mk("letter")
	if def != strings.Replace(a4, "", "", 0) || box.FindStringSubmatch(def)[1] != "595" || box.FindStringSubmatch(def)[2] != "842" {
		t.Fatal("default must stay A4 portrait and equal explicit a4")
	}
	if g := box.FindStringSubmatch(land); g[1] != "842" || g[2] != "595" {
		t.Fatalf("landscape box %v", g)
	}
	if g := box.FindStringSubmatch(letter); g[1] != "612" || g[2] != "792" {
		t.Fatalf("letter box %v", g)
	}
	if pages(land) <= pages(def) || pages(letter) < pages(def) {
		t.Fatalf("shorter pages must not have fewer pages: a4=%d land=%d letter=%d", pages(def), pages(land), pages(letter))
	}
	if !strings.Contains(land, "Page 1 of ") || !strings.Contains(land, "(H)") || !strings.Contains(land, "(F)") {
		t.Fatal("header/footer/page numbers missing")
	}
	bad := Definition{Metrics: []Metric{m}, WindowHours: 24, GroupBy: "hour", Page: "tabloid"}
	if Validate(bad) == nil {
		t.Fatal("unknown page accepted")
	}
	d, err := ApplyParams(Definition{Metrics: []Metric{m}, WindowHours: 24, GroupBy: "hour"}, func(k string) string {
		if k == "page" {
			return "letter-landscape"
		}
		return ""
	})
	if err != nil || d.Page != "letter-landscape" {
		t.Fatalf("page param: %v %q", err, d.Page)
	}
}
