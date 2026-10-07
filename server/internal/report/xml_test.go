package report

import (
	"encoding/xml"
	"math"
	"strings"
	"testing"
	"time"
)

func TestRenderXML(t *testing.T) {
	m := Metric{DeviceID: "d1", PointID: "p1"}
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	d := Definition{Metrics: []Metric{m}, WindowHours: 24, GroupBy: "hour"}
	series := map[Metric][]Bucket{m: {{Start: t0, Avg: 1.5, Min: 1, Max: 2, Sum: 3, Count: 2}, {Start: t0.Add(time.Hour), Avg: math.NaN(), Min: math.Inf(1), Count: 0}}}
	b, err := RenderXML(`A "T" <&>`, d, series, t0)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Title  string `xml:"title,attr"`
		Series []struct {
			Dev     string `xml:"device_id,attr"`
			Buckets []struct {
				Avg string `xml:"avg,attr"`
				Cnt int    `xml:"count,attr"`
			} `xml:"bucket"`
		} `xml:"series"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("not well formed: %v\n%s", err, b)
	}
	if doc.Title != `A "T" <&>` || len(doc.Series) != 1 || len(doc.Series[0].Buckets) != 2 || doc.Series[0].Buckets[0].Avg != "1.5" || doc.Series[0].Buckets[1].Avg != "" {
		t.Fatalf("bad content: %+v", doc)
	}
	if strings.Contains(string(b), "NaN") || strings.Contains(string(b), "Inf") {
		t.Fatal("non-finite leaked")
	}
}
