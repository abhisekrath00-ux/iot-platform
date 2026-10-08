package report

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
)

// Plain PNG charts for Word and PowerPoint, drawn with the standard library only (no fonts, no
// external services). The picture has gridlines and the series but no text: the caption that goes
// with it states the range and the first and last bucket. Line, area, bar and scatter are drawn;
// gauge and pie are not (they are in the HTML and PDF reports).
const (
	pngW = 1200
	pngH = 420
)

func pngChartKind(k string) bool { return k == "line" || k == "area" || k == "bar" || k == "scatter" }

// ChartPNG returns the picture and its caption, or nil when there are fewer than 2 finite buckets.
func ChartPNG(kind string, rows []Bucket) ([]byte, string) {
	if !pngChartKind(kind) {
		return nil, ""
	}
	var vs []float64
	var first, last string
	for _, r := range rows {
		if math.IsNaN(r.Avg) || math.IsInf(r.Avg, 0) {
			continue
		}
		if len(vs) == 0 {
			first = r.Start.UTC().Format("2006-01-02 15:04")
		}
		last = r.Start.UTC().Format("2006-01-02 15:04")
		vs = append(vs, r.Avg)
	}
	if len(vs) < 2 {
		return nil, ""
	}
	n := len(vs)
	if n > maxChartPoints {
		step := float64(n) / float64(maxChartPoints)
		out := make([]float64, 0, maxChartPoints)
		for i := 0; i < maxChartPoints; i++ {
			out = append(out, vs[int(float64(i)*step)])
		}
		vs = out
	}
	lo, hi := vs[0], vs[0]
	for _, v := range vs {
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	axisLo, axisHi := lo, hi
	if kind == "bar" || kind == "area" {
		axisLo = math.Min(lo, 0)
	}
	if axisHi == axisLo {
		axisHi, axisLo = axisHi+1, axisLo-1
	}
	pad := (axisHi - axisLo) * 0.06
	if axisLo != 0 || kind == "line" || kind == "scatter" {
		axisLo -= pad
	}
	axisHi += pad

	img := image.NewRGBA(image.Rect(0, 0, pngW, pngH))
	fill(img, image.Rect(0, 0, pngW, pngH), color.RGBA{255, 255, 255, 255})
	left, right, top, bottom := 24, pngW-24, 20, pngH-24
	grid := color.RGBA{225, 228, 232, 255}
	for i := 0; i <= 5; i++ {
		y := top + (bottom-top)*i/5
		fill(img, image.Rect(left, y, right, y+1), grid)
	}
	fill(img, image.Rect(left, top, left+1, bottom+1), color.RGBA{150, 155, 165, 255})
	fill(img, image.Rect(left, bottom, right+1, bottom+1), color.RGBA{150, 155, 165, 255})
	blue := color.RGBA{37, 99, 235, 255}
	xAt := func(i int) int { return left + 12 + (right-left-24)*i/(len(vs)-1) }
	yAt := func(v float64) int { return bottom - int(float64(bottom-top)*(v-axisLo)/(axisHi-axisLo)) }
	switch kind {
	case "bar":
		bw := (right - left - 24) / len(vs)
		if bw < 2 {
			bw = 2
		}
		zero := yAt(math.Max(axisLo, 0))
		for i, v := range vs {
			x := left + 12 + (right-left-24)*i/len(vs)
			y := yAt(v)
			y0, y1 := y, zero
			if y0 > y1 {
				y0, y1 = y1, y0
			}
			fill(img, image.Rect(x+1, y0, x+bw-1, y1+1), blue)
		}
	case "scatter":
		for i, v := range vs {
			disc(img, xAt(i), yAt(v), 4, blue)
		}
	default:
		if kind == "area" {
			light := color.RGBA{191, 211, 250, 255}
			for i := 0; i < len(vs)-1; i++ {
				x0, x1 := xAt(i), xAt(i+1)
				for x := x0; x <= x1; x++ {
					t := 0.0
					if x1 > x0 {
						t = float64(x-x0) / float64(x1-x0)
					}
					y := int(float64(yAt(vs[i]))*(1-t) + float64(yAt(vs[i+1]))*t)
					fill(img, image.Rect(x, y, x+1, bottom), light)
				}
			}
		}
		for i := 0; i < len(vs)-1; i++ {
			thickLine(img, xAt(i), yAt(vs[i]), xAt(i+1), yAt(vs[i+1]), 2, blue)
		}
	}
	var buf bytes.Buffer
	if png.Encode(&buf, img) != nil {
		return nil, ""
	}
	return buf.Bytes(), fmt.Sprintf("Average per bucket: lowest %.3f, highest %.3f. First bucket %s, last bucket %s (UTC), %d buckets.", lo, hi, first, last, n)
}

func fill(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	r = r.Intersect(img.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

func disc(img *image.RGBA, cx, cy, rad int, c color.RGBA) {
	for y := -rad; y <= rad; y++ {
		for x := -rad; x <= rad; x++ {
			if x*x+y*y <= rad*rad && image.Pt(cx+x, cy+y).In(img.Bounds()) {
				img.SetRGBA(cx+x, cy+y, c)
			}
		}
	}
}

func thickLine(img *image.RGBA, x0, y0, x1, y1, rad int, c color.RGBA) {
	dx, dy := float64(x1-x0), float64(y1-y0)
	steps := int(math.Max(math.Abs(dx), math.Abs(dy)))
	if steps == 0 {
		disc(img, x0, y0, rad, c)
		return
	}
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		disc(img, x0+int(dx*t), y0+int(dy*t), rad, c)
	}
}
