package report

import (
	"fmt"
	"html"
	"math"
	"sort"
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
	case "", "line", "area", "bar", "scatter", "gauge", "pie":
		return true
	}
	return false
}

// chartSVG draws one metric's per-bucket averages. Non-finite values are skipped; fewer than two
// finite points yields a text note instead of a chart, never a broken drawing.
func chartSVG(kind string, rows []Bucket, dark bool) string {
	switch kind {
	case "gauge":
		return gaugeSVG(rows, dark)
	case "pie":
		return pieSVG(rows, dark)
	}
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
			cx := l + pw*(float64(i)+0.5)/float64(len(pts))
			fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="%s"/>`, cx-bw/2, top, bw, math.Max(h, 0.5), series)
		}
	case "scatter":
		for i, p := range pts {
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="2.5" fill="%s" fill-opacity="0.8"/>`, x(i), y(p.v), series)
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

func palette(dark bool) []string {
	if dark {
		return []string{"#60a5fa", "#34d399", "#fbbf24", "#f87171", "#a78bfa", "#f472b6", "#22d3ee", "#a3e635"}
	}
	return []string{"#2563eb", "#059669", "#d97706", "#dc2626", "#7c3aed", "#db2777", "#0891b2", "#65a30d"}
}

// gaugeSVG shows the latest finite bucket average on a half-circle spanning the series min..max.
func gaugeSVG(rows []Bucket, dark bool) string {
	var last float64
	found := false
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, r := range rows {
		if math.IsNaN(r.Avg) || math.IsInf(r.Avg, 0) {
			continue
		}
		last, found = r.Avg, true
		lo, hi = math.Min(lo, r.Avg), math.Max(hi, r.Avg)
	}
	if !found {
		return `<p class="meta">not enough data to chart</p>`
	}
	if hi == lo {
		lo, hi = lo-1, hi+1
	}
	ink, track := "#444", "#ddd"
	if dark {
		ink, track = "#c8cad0", "#3a3d45"
	}
	frac := (last - lo) / (hi - lo)
	const cx, cy, rad = 160.0, 140.0, 110.0
	pt := func(f float64) (float64, float64) {
		a := math.Pi * (1 - f)
		return cx + rad*math.Cos(a), cy - rad*math.Sin(a)
	}
	x0, y0 := pt(0)
	x1, y1 := pt(1)
	xv, yv := pt(frac)
	col := palette(dark)[0]
	var b strings.Builder
	fmt.Fprintf(&b, `<svg role="img" aria-label="gauge chart" viewBox="0 0 320 170" width="100%%" style="max-width:320px" xmlns="http://www.w3.org/2000/svg">`)
	fmt.Fprintf(&b, `<path d="M%.1f %.1f A%.0f %.0f 0 0 1 %.1f %.1f" fill="none" stroke="%s" stroke-width="18"/>`, x0, y0, rad, rad, x1, y1, track)
	if frac > 0.001 {
		fmt.Fprintf(&b, `<path d="M%.1f %.1f A%.0f %.0f 0 0 1 %.1f %.1f" fill="none" stroke="%s" stroke-width="18"/>`, x0, y0, rad, rad, xv, yv, col)
	}
	fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" font-size="28" text-anchor="middle" fill="%s">%s</text>`, cx, cy-10, ink, html.EscapeString(fmt.Sprintf("%.4g", last)))
	fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" font-size="10" text-anchor="middle" fill="%s">%s</text><text x="%.0f" y="%.0f" font-size="10" text-anchor="middle" fill="%s">%s</text>`,
		cx-rad, cy+16, ink, html.EscapeString(fmt.Sprintf("%.3g", lo)), cx+rad, cy+16, ink, html.EscapeString(fmt.Sprintf("%.3g", hi)))
	b.WriteString(`</svg>`)
	return b.String()
}

// pieSVG shows each bucket's share of the window total (sum of per-bucket sums, positive only).
// More than eight buckets are folded: the largest seven stay, the rest become "other".
func pieSVG(rows []Bucket, dark bool) string {
	type sl struct {
		label string
		v     float64
	}
	var all []sl
	total := 0.0
	for _, r := range rows {
		if math.IsNaN(r.Sum) || math.IsInf(r.Sum, 0) || r.Sum <= 0 {
			continue
		}
		all = append(all, sl{r.Start.UTC().Format("01-02 15:04"), r.Sum})
		total += r.Sum
	}
	if len(all) < 2 || total <= 0 {
		return `<p class="meta">not enough data to chart</p>`
	}
	if len(all) > 8 {
		sorted := append([]sl(nil), all...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].v > sorted[j].v })
		keep := map[string]bool{}
		for _, s := range sorted[:7] {
			keep[s.label] = true
		}
		var out []sl
		other := 0.0
		for _, s := range all {
			if keep[s.label] {
				out = append(out, s)
			} else {
				other += s.v
			}
		}
		all = append(out, sl{"other", other})
	}
	cols := palette(dark)
	ink := "#444"
	if dark {
		ink = "#c8cad0"
	}
	const cx, cy, rad = 90.0, 90.0, 80.0
	var b strings.Builder
	fmt.Fprintf(&b, `<svg role="img" aria-label="pie chart" viewBox="0 0 330 180" width="100%%" style="max-width:330px" xmlns="http://www.w3.org/2000/svg">`)
	ang := -math.Pi / 2
	for i, s := range all {
		sweep := 2 * math.Pi * s.v / total
		c := cols[i%len(cols)]
		if sweep >= 2*math.Pi-1e-9 {
			fmt.Fprintf(&b, `<circle cx="%.0f" cy="%.0f" r="%.0f" fill="%s"/>`, cx, cy, rad, c)
		} else {
			xa, ya := cx+rad*math.Cos(ang), cy+rad*math.Sin(ang)
			xb, yb := cx+rad*math.Cos(ang+sweep), cy+rad*math.Sin(ang+sweep)
			large := 0
			if sweep > math.Pi {
				large = 1
			}
			fmt.Fprintf(&b, `<path d="M%.0f %.0f L%.1f %.1f A%.0f %.0f 0 %d 1 %.1f %.1f Z" fill="%s"/>`, cx, cy, xa, ya, rad, rad, large, xb, yb, c)
		}
		ang += sweep
		fmt.Fprintf(&b, `<rect x="190" y="%d" width="9" height="9" fill="%s"/><text x="204" y="%d" font-size="10" fill="%s">%s %.1f%%</text>`,
			10+i*16, c, 18+i*16, ink, html.EscapeString(s.label), 100*s.v/total)
	}
	b.WriteString(`</svg>`)
	return b.String()
}
