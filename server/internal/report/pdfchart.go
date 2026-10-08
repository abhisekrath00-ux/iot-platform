package report

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// chartOpsKind draws the chart type the report asks for with PDF vector operators. "" and "line" keep the original
// line chart (chartOps) byte for byte; bar, area, scatter, gauge and pie are new. Same box and slot count as the line
// chart. Gauge shows the latest bucket average, pie the share of each bucket's sum, as in the HTML report.
func chartOpsKind(kind string, c *strings.Builder, rows []Bucket, title string, y int, esc func(string) string, dark bool, w float64) {
	switch kind {
	case "bar", "area", "scatter", "gauge", "pie":
	default:
		chartOps(c, rows, title, y, esc, dark, w)
		return
	}
	const x0, h = 70.0, 100.0
	top := float64(y) - 4
	bottom := top - h - 14
	plotTop := top - 12
	frame := "0.6 G"
	if dark {
		frame = "0.45 G"
	}
	label := map[string]string{"bar": "avg per bucket", "area": "avg", "scatter": "avg", "gauge": "latest avg", "pie": "share of bucket sums"}[kind]
	fmt.Fprintf(c, "BT /F2 8 Tf %.0f %.1f Td (%s) Tj ET\n", x0, top-8, esc(title+"  ("+label+")"))
	ink := "0 g"
	if dark {
		ink = "0.92 g"
	}
	switch kind {
	case "gauge":
		pdfGauge(c, rows, x0, bottom, plotTop, w, esc, dark, ink)
	case "pie":
		pdfPie(c, rows, x0, bottom, plotTop, esc, dark, ink)
	default:
		pdfXY(kind, c, rows, x0, bottom, plotTop, w, esc, dark, frame)
	}
	if dark {
		c.WriteString("0.92 g 0.92 G 1 w\n")
	} else {
		c.WriteString("0 g 0 G 1 w\n")
	}
}

func hexRGB(h string) string {
	if len(h) != 7 || h[0] != '#' {
		return "0 0 0"
	}
	v, err := strconv.ParseUint(h[1:], 16, 32)
	if err != nil {
		return "0 0 0"
	}
	return fmt.Sprintf("%.3f %.3f %.3f", float64(v>>16&255)/255, float64(v>>8&255)/255, float64(v&255)/255)
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func pdfXY(kind string, c *strings.Builder, rows []Bucket, x0, bottom, plotTop, w float64, esc func(string) string, dark bool, frame string) {
	type pt struct {
		t string
		v float64
	}
	var pts []pt
	for _, r := range rows {
		if finite(r.Avg) {
			pts = append(pts, pt{r.Start.UTC().Format("2006-01-02 15:04"), r.Avg})
		}
	}
	if len(pts) == 0 {
		return
	}
	if len(pts) > 240 { // thin evenly, as the line chart does
		step := float64(len(pts)) / 240
		out := make([]pt, 0, 240)
		for i := 0; i < 240; i++ {
			out = append(out, pts[int(float64(i)*step)])
		}
		pts = out
	}
	lo, hi := pts[0].v, pts[0].v
	for _, p := range pts {
		lo, hi = math.Min(lo, p.v), math.Max(hi, p.v)
	}
	if kind == "bar" || kind == "area" {
		lo = math.Min(lo, 0) // bars and areas start at zero so heights are honest
		hi = math.Max(hi, 0)
	}
	if hi == lo {
		hi, lo = hi+1, lo-1
	}
	py := func(v float64) float64 { return bottom + (v-lo)/(hi-lo)*(plotTop-bottom) }
	px := func(i int) float64 {
		if len(pts) == 1 {
			return x0 + w/2
		}
		return x0 + float64(i)/float64(len(pts)-1)*w
	}
	col := hexRGB(palette(dark)[0])
	fmt.Fprintf(c, "%s 0.5 w %.1f %.1f %.1f %.1f re S\n", frame, x0, bottom, w, plotTop-bottom)
	switch kind {
	case "bar":
		bw := math.Max(w/float64(len(pts))*0.7, 0.6)
		fmt.Fprintf(c, "%s rg\n", col)
		for i, p := range pts {
			a, b := py(0), py(p.v)
			if a > b {
				a, b = b, a
			}
			fmt.Fprintf(c, "%.1f %.1f %.1f %.1f re f\n", px(i)-bw/2, a, bw, math.Max(b-a, 0.3))
		}
	case "area":
		fmt.Fprintf(c, "%s rg\n%.1f %.1f m\n", col, px(0), py(0))
		for i, p := range pts {
			fmt.Fprintf(c, "%.1f %.1f l\n", px(i), py(p.v))
		}
		fmt.Fprintf(c, "%.1f %.1f l f\n", px(len(pts)-1), py(0))
	case "scatter":
		fmt.Fprintf(c, "%s rg\n", col)
		for i, p := range pts {
			fmt.Fprintf(c, "%.1f %.1f 2 2 re f\n", px(i)-1, py(p.v)-1)
		}
	}
	if dark {
		c.WriteString("0.92 g\n")
	} else {
		c.WriteString("0 g\n")
	}
	fmt.Fprintf(c, "BT /F1 7 Tf 40 %.1f Td (%s) Tj ET\n", plotTop-6, esc(fmt.Sprintf("%.4g", hi)))
	fmt.Fprintf(c, "BT /F1 7 Tf 40 %.1f Td (%s) Tj ET\n", bottom, esc(fmt.Sprintf("%.4g", lo)))
	fmt.Fprintf(c, "BT /F1 7 Tf %.0f %.1f Td (%s) Tj ET\n", x0, bottom-8, esc(pts[0].t))
	fmt.Fprintf(c, "BT /F1 7 Tf %.0f %.1f Td (%s) Tj ET\n", x0+w-80, bottom-8, esc(pts[len(pts)-1].t))
}

func arcPoints(cx, cy, r, a0, a1 float64, n int) string {
	var b strings.Builder
	for i := 0; i <= n; i++ {
		a := a0 + (a1-a0)*float64(i)/float64(n)
		op := "l"
		if i == 0 {
			op = "m"
		}
		fmt.Fprintf(&b, "%.1f %.1f %s\n", cx+r*math.Cos(a), cy+r*math.Sin(a), op)
	}
	return b.String()
}

func pdfGauge(c *strings.Builder, rows []Bucket, x0, bottom, plotTop, w float64, esc func(string) string, dark bool, ink string) {
	var last float64
	found := false
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, r := range rows {
		if finite(r.Avg) {
			last, found = r.Avg, true
			lo, hi = math.Min(lo, r.Avg), math.Max(hi, r.Avg)
		}
	}
	if !found {
		return
	}
	if hi == lo {
		lo, hi = lo-1, hi+1
	}
	frac := (last - lo) / (hi - lo)
	rad := math.Min((plotTop-bottom)-14, w/2)
	cx, cy := x0+w/2, bottom+10
	track := "0.87 G"
	if dark {
		track = "0.24 G"
	}
	fmt.Fprintf(c, "%s 12 w\n%sS\n", track, arcPoints(cx, cy, rad, math.Pi, 0, 40))
	if frac > 0.002 {
		fmt.Fprintf(c, "%s RG 12 w\n%sS\n", hexRGB(palette(dark)[0]), arcPoints(cx, cy, rad, math.Pi, math.Pi*(1-frac), int(math.Max(4, 40*frac))))
	}
	fmt.Fprintf(c, "%s\nBT /F2 16 Tf %.1f %.1f Td (%s) Tj ET\n", ink, cx-24, cy+4, esc(fmt.Sprintf("%.4g", last)))
	fmt.Fprintf(c, "BT /F1 7 Tf %.1f %.1f Td (%s) Tj ET\nBT /F1 7 Tf %.1f %.1f Td (%s) Tj ET\n", cx-rad-6, cy-12, esc(fmt.Sprintf("%.3g", lo)), cx+rad-10, cy-12, esc(fmt.Sprintf("%.3g", hi)))
}

func pdfPie(c *strings.Builder, rows []Bucket, x0, bottom, plotTop float64, esc func(string) string, dark bool, ink string) {
	type sl struct {
		label string
		v     float64
	}
	var all []sl
	total := 0.0
	for _, r := range rows {
		if finite(r.Sum) && r.Sum > 0 {
			all = append(all, sl{r.Start.UTC().Format("01-02 15:04"), r.Sum})
			total += r.Sum
		}
	}
	if len(all) < 2 || total <= 0 {
		return
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
	rad := math.Min((plotTop-bottom)/2-2, 46)
	cx, cy := x0+rad+4, bottom+(plotTop-bottom)/2
	cols := palette(dark)
	ang := math.Pi / 2
	for i, s := range all {
		sweep := 2 * math.Pi * s.v / total
		fmt.Fprintf(c, "%s rg\n%.1f %.1f m\n%s", hexRGB(cols[i%len(cols)]), cx, cy, strings.Replace(arcPoints(cx, cy, rad, ang, ang-sweep, int(math.Max(3, 40*sweep/(2*math.Pi)))), " m\n", " l\n", 1))
		c.WriteString("f\n")
		ang -= sweep
		ly := plotTop - 8 - float64(i)*10
		fmt.Fprintf(c, "%s rg %.1f %.1f 6 6 re f\n%s\nBT /F1 7 Tf %.1f %.1f Td (%s) Tj ET\n", hexRGB(cols[i%len(cols)]), x0+2*rad+24, ly, ink, x0+2*rad+34, ly, esc(fmt.Sprintf("%s  %.1f%%", s.label, 100*s.v/total)))
	}
}
