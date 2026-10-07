package report

import (
	"fmt"
	"html"
	"math"
	"strings"
)

// Chart kinds drawn into the HTML report as inline SVG (no scripts, no external assets).
// Empty means no chart, which keeps every existing report unchanged.
const (
	maxChartPoints = 400
	chartW         = 720
	chartH         = 220
)

func validChart(k string) bool {
	switch k {
	case "", "line", "area", "bar":
		return true
	}
	return false
}

// chartSVG draws one metric's per-bucket averages. Non-finite values are skipped; fewer than two
// finite points yields a text note instead of a chart, never a broken drawing.
func chartSVG(kind string, rows []Bucket, dark bool) string {
	type pt struct {
		label string
		v     float64
	}
	var pts []pt
	for _, r := range rows {
		if math.IsNaN(r.Avg) || math.IsInf(r.Avg, 0) {
			continue
		}
		pts = append(pts, pt{r.Start.UTC().Format("01-02 15:04"), r.Avg})
	}
	if len(pts) < 2 {
		return `<p class="meta">not enough data to chart</p>`
	}
	if len(pts) > maxChartPoints { // thin evenly; the table still lists every bucket
		step := float64(len(pts)) / float64(maxChartPoints)
		out := make([]pt, 0, maxChartPoints)
		for i := 0; i < maxChartPoints; i++ {
			out = append(out, pts[int(float64(i)*step)])
		}
		pts = out
	}
	lo, hi := pts[0].v, pts[0].v
	for _, p := range pts {
		lo, hi = math.Min(lo, p.v), math.Max(hi, p.v)
	}
	if kind == "bar" || kind == "area" {
		lo = math.Min(lo, 0)
	}
	if hi == lo {
		hi = lo + 1
	}
	const l, r, t, bt = 56.0, 12.0, 10.0, 28.0
	pw, ph := chartW-l-r, chartH-t-bt
	x := func(i int) float64 {
		if len(pts) == 1 {
			return l
		}
		return l + pw*float64(i)/float64(len(pts)-1)
	}
	y := func(v float64) float64 { return t + ph*(1-(v-lo)/(hi-lo)) }
	ink, grid, series := "#444", "#ddd", "#2563eb"
	if dark {
		ink, grid, series = "#c8cad0", "#3a3d45", "#60a5fa"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg role="img" aria-label="%s chart" viewBox="0 0 %d %d" width="100%%" style="max-width:%dpx" xmlns="http://www.w3.org/2000/svg">`, html.EscapeString(kind), chartW, chartH, chartW)
	for i := 0; i <= 4; i++ {
		v := lo + (hi-lo)*float64(i)/4
		fmt.Fprintf(&b, `<line x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f" stroke="%s"/><text x="%.1f" y="%.1f" font-size="10" text-anchor="end" fill="%s">%s</text>`,
			l, float64(chartW)-r, y(v), y(v), grid, l-4, y(v)+3, ink, html.EscapeString(fmt.Sprintf("%.3g", v)))
	}
	fmt.Fprintf(&b, `<text x="%.1f" y="%d" font-size="10" fill="%s">%s</text><text x="%.1f" y="%d" font-size="10" text-anchor="end" fill="%s">%s</text>`,
		l, chartH-8, ink, html.EscapeString(pts[0].label), float64(chartW)-r, chartH-8, ink, html.EscapeString(pts[len(pts)-1].label))
	base := y(math.Max(lo, math.Min(0, hi)))
	switch kind {
	case "bar":
		bw := math.Max(1, pw/float64(len(pts))*0.8)
		for i, p := range pts {
			top, h := y(p.v), base-y(p.v)
			if h < 0 {
				top, h = base, -h
			}
			fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="%s"/>`, x(i)-bw/2, top, bw, math.Max(h, 0.5), series)
		}
	default:
		var line strings.Builder
		for i, p := range pts {
			fmt.Fprintf(&line, "%.1f,%.1f ", x(i), y(p.v))
		}
		if kind == "area" {
			fmt.Fprintf(&b, `<polygon points="%.1f,%.1f %s%.1f,%.1f" fill="%s" fill-opacity="0.25"/>`, x(0), base, line.String(), x(len(pts)-1), base, series)
		}
		fmt.Fprintf(&b, `<polyline points="%s" fill="none" stroke="%s" stroke-width="1.8"/>`, strings.TrimSpace(line.String()), series)
	}
	b.WriteString(`</svg>`)
	return b.String()
}
