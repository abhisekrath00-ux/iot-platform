package report

import (
	"strings"
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	ok := Definition{Metrics: []Metric{{"meter-1", "kwh"}}, WindowHours: 24, GroupBy: "hour"}
	if err := Validate(ok); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		d    Definition
	}{
		{"no metrics", Definition{WindowHours: 24, GroupBy: "hour"}},
		{"bad window", Definition{Metrics: ok.Metrics, WindowHours: 0, GroupBy: "hour"}},
		{"bad group", Definition{Metrics: ok.Metrics, WindowHours: 24, GroupBy: "minute"}},
		{"sql-ish id", Definition{Metrics: []Metric{{"x'; DROP TABLE--", "kwh"}}, WindowHours: 24, GroupBy: "hour"}},
		{"html id", Definition{Metrics: []Metric{{"<script>", "kwh"}}, WindowHours: 24, GroupBy: "hour"}},
	}
	for _, c := range cases {
		if err := Validate(c.d); err == nil {
			t.Fatalf("%s: accepted", c.name)
		}
	}
}

func TestRenderEscapesAndFormats(t *testing.T) {
	d := Definition{Metrics: []Metric{{"meter-1", "kwh"}}, WindowHours: 24, GroupBy: "hour"}
	series := map[Metric][]Bucket{
		{"meter-1", "kwh"}: {{Start: time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC), Avg: 42.5, Min: 1, Max: 99, Count: 60}},
	}
	out := Render("Energy <Daily>", d, series, time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))
	if !strings.Contains(out, "Energy &lt;Daily&gt;") {
		t.Fatal("title not escaped")
	}
	if !strings.Contains(out, "42.500") || !strings.Contains(out, "2026-09-27 10:04") == false && !strings.Contains(out, "2026-09-27 10:00") {
		t.Fatal("missing bucket row")
	}
	if strings.Contains(out, "http://") || strings.Contains(out, "https://") || strings.Contains(out, "<script") {
		t.Fatal("external reference or script in output")
	}
}

func TestRenderEmptySeries(t *testing.T) {
	d := Definition{Metrics: []Metric{{"meter-1", "kwh"}}, WindowHours: 24, GroupBy: "day"}
	out := Render("t", d, map[Metric][]Bucket{{"meter-1", "kwh"}: nil}, time.Now())
	if !strings.Contains(out, "no data in window") {
		t.Fatal("missing empty note")
	}
}

func TestCSVSafeAndRender(t *testing.T) {
	if CSVSafe("=cmd|'/c calc'") != "'=cmd|'/c calc'" || CSVSafe("ok") != "ok" || CSVSafe("") != "" {
		t.Fatal("CSVSafe wrong")
	}
	m := Metric{DeviceID: "d1", PointID: "t"}
	out := RenderCSV(Definition{Metrics: []Metric{m}}, map[Metric][]Bucket{m: {{Start: time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC), Avg: 1.5, Min: 1, Max: 2, Sum: 6, Count: 4}}})
	want := "device_id,point_id,bucket_start,avg,min,max,count,sum\nd1,t,2026-01-02T03:00:00Z,1.5,1,2,4,6\n"
	if out != want {
		t.Fatalf("got %q", out)
	}
}

func TestGroupByWhitelistAndSummary(t *testing.T) {
	for _, g := range []string{"15min", "hour", "day", "week"} {
		if BucketExpr(g) == "" {
			t.Fatalf("%s must be allowed", g)
		}
		d := Definition{Metrics: []Metric{{"d1", "t"}}, WindowHours: 24, GroupBy: g}
		if err := Validate(d); err != nil {
			t.Fatalf("%s: %v", g, err)
		}
	}
	for _, g := range []string{"", "minute", "hour'; DROP TABLE telemetry;--", "month"} {
		if BucketExpr(g) != "" {
			t.Fatalf("%q must be rejected", g)
		}
	}
	avg, mn, mx, sum, n := Summary([]Bucket{{Min: 1, Max: 5, Sum: 6, Count: 3}, {Min: 0, Max: 9, Sum: 14, Count: 2}})
	if mn != 0 || mx != 9 || sum != 20 || n != 5 || avg != 4 {
		t.Fatalf("summary wrong: %v %v %v %v %v", avg, mn, mx, sum, n)
	}
	if a, _, _, _, n := Summary(nil); a != 0 || n != 0 {
		t.Fatal("empty summary must be zero")
	}
}
