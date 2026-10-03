package report

import (
	"fmt"
	"html"
	"math"
	"sort"
	"strings"
	"time"
)

// Insight is a plain statistical reading of one metric's bucketed averages. It describes the data in the
// window. It does not explain why anything happened and it never predicts.
type Insight struct {
	Metric    Metric
	Samples   int
	Avg       float64
	Min, Max  float64
	Trend     string  // rising | falling | flat | "" (fewer than 3 buckets)
	TrendPct  float64 // change across the window from a straight-line fit, % of the mean (absolute when the mean is near 0)
	PeakAt    time.Time
	PeakValue float64
	Unusual   []Bucket // buckets whose average is far from the median (robust z above 3.5), at most 3
	UnusualN  int
	HasPrev   bool
	PrevAvg   float64
	ChangePct float64 // current avg vs previous window avg, % (absolute difference when the previous avg is near 0)
}

const (
	trendFlatPct = 5.0
	outlierZ     = 3.5
)

func pct(delta, base float64) float64 {
	if math.Abs(base) < 1e-9 {
		return delta
	}
	return delta / math.Abs(base) * 100
}

// Insights computes one Insight per metric that has data, in the order the metrics were defined.
func Insights(d Definition, series map[Metric][]Bucket) []Insight {
	var out []Insight
	for _, m := range d.Metrics {
		rows := series[m]
		if len(rows) == 0 {
			continue
		}
		avg, mn, mx, _, n := Summary(rows)
		in := Insight{Metric: m, Samples: n, Avg: avg, Min: mn, Max: mx}
		pk := rows[0]
		for _, r := range rows {
			if r.Max > pk.Max {
				pk = r
			}
		}
		in.PeakAt, in.PeakValue = pk.Start, pk.Max
		if len(rows) >= 3 {
			xs := make([]float64, len(rows))
			var mean float64
			for i, r := range rows {
				xs[i] = r.Avg
				mean += r.Avg
			}
			mean /= float64(len(rows))
			var sxy, sxx float64
			mx0 := float64(len(rows)-1) / 2
			for i, y := range xs {
				sxy += (float64(i) - mx0) * (y - mean)
				sxx += (float64(i) - mx0) * (float64(i) - mx0)
			}
			change := sxy / sxx * float64(len(rows)-1)
			in.TrendPct = pct(change, mean)
			switch {
			case math.Abs(in.TrendPct) < trendFlatPct:
				in.Trend = "flat"
			case in.TrendPct > 0:
				in.Trend = "rising"
			default:
				in.Trend = "falling"
			}
			// robust outliers: median and median absolute deviation of the bucket averages
			med := median(xs)
			dev := make([]float64, len(xs))
			for i, y := range xs {
				dev[i] = math.Abs(y - med)
			}
			if mad := median(dev); mad > 0 {
				for i, y := range xs {
					if math.Abs(0.6745*(y-med)/mad) > outlierZ {
						in.UnusualN++
						if len(in.Unusual) < 3 {
							in.Unusual = append(in.Unusual, rows[i])
						}
					}
				}
			}
		}
		if prev := d.Previous[m]; len(prev) > 0 {
			pa, _, _, _, _ := Summary(prev)
			in.HasPrev, in.PrevAvg, in.ChangePct = true, pa, pct(avg-pa, pa)
		}
		out = append(out, in)
	}
	return out
}

func median(v []float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

const insightsNote = "Statistical summary of bucketed averages in this window. It describes what the data did, not why, and it predicts nothing."

// InsightLines is the text form used by the PDF.
func InsightLines(ins []Insight) []string {
	lines := []string{}
	for _, i := range ins {
		s := fmt.Sprintf("%s / %s: avg %.3g (min %.3g, max %.3g)", i.Metric.DeviceID, i.Metric.PointID, i.Avg, i.Min, i.Max)
		if i.Trend != "" {
			s += fmt.Sprintf("; %s %+.1f%%", i.Trend, i.TrendPct)
		}
		if i.HasPrev {
			s += fmt.Sprintf("; vs previous window %+.1f%% (was %.3g)", i.ChangePct, i.PrevAvg)
		}
		lines = append(lines, s)
		pk := fmt.Sprintf("   peak %.3g at %s", i.PeakValue, i.PeakAt.UTC().Format("2006-01-02 15:04"))
		if i.UnusualN > 0 {
			pk += fmt.Sprintf("; %d unusual bucket(s)", i.UnusualN)
		}
		lines = append(lines, pk)
	}
	return lines
}

func writeInsightsHTML(b *strings.Builder, d Definition, series map[Metric][]Bucket) {
	if !d.Insights {
		return
	}
	ins := Insights(d, series)
	b.WriteString(`<h2>Insights</h2><p class="meta">` + html.EscapeString(insightsNote) + `</p>`)
	if len(ins) == 0 {
		b.WriteString(`<p class="meta">no data in window</p>`)
		return
	}
	b.WriteString(`<table><tr><th>metric</th><th>avg</th><th>trend</th><th>peak</th><th>unusual buckets</th>`)
	if d.Compare {
		b.WriteString(`<th>vs previous window</th>`)
	}
	b.WriteString(`</tr>`)
	for _, i := range ins {
		tr := "-"
		if i.Trend != "" {
			tr = fmt.Sprintf("%s (%+.1f%%)", i.Trend, i.TrendPct)
		}
		un := "none"
		if i.UnusualN > 0 {
			parts := []string{}
			for _, u := range i.Unusual {
				parts = append(parts, fmt.Sprintf("%s: %.3g", u.Start.UTC().Format("01-02 15:04"), u.Avg))
			}
			un = fmt.Sprintf("%d (%s)", i.UnusualN, strings.Join(parts, "; "))
		}
		fmt.Fprintf(b, `<tr><td>%s / %s</td><td>%.3g</td><td>%s</td><td>%.3g at %s</td><td>%s</td>`,
			html.EscapeString(i.Metric.DeviceID), html.EscapeString(i.Metric.PointID), i.Avg, tr, i.PeakValue, i.PeakAt.UTC().Format("2006-01-02 15:04"), html.EscapeString(un))
		if d.Compare {
			if i.HasPrev {
				fmt.Fprintf(b, `<td>%+.1f%% (was %.3g)</td>`, i.ChangePct, i.PrevAvg)
			} else {
				b.WriteString(`<td>no earlier data</td>`)
			}
		}
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</table>`)
}
