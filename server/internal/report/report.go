// Package report validates report definitions and renders telemetry
// aggregates into a self-contained HTML document (inline CSS, no external
// assets: it must render in air-gapped mail clients and browsers).
package report

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/kpi"
	"html"
	"image"
	"image/png"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Sample is one raw reading shown in a drill-through list.
type Sample struct {
	At    time.Time
	Value float64
}

// MaxDetailBuckets caps the drill-through sections per metric.
const MaxDetailBuckets = 10

type Metric struct {
	DeviceID string `json:"device_id"`
	PointID  string `json:"point_id"`
}

type Definition struct {
	Metrics     []Metric `json:"metrics"`
	WindowHours int      `json:"window_hours"`
	GroupBy     string   `json:"group_by"`         // 15min|hour|day|week
	Layout      string   `json:"layout,omitempty"` // "" (one table per metric) or "matrix" (bucket rows x metric columns)
	Agg         string   `json:"agg,omitempty"`    // matrix cell value: avg (default), min, max, sum
	// Computed adds derived columns to a matrix, e.g. "{meter-1.kwh} / {line-a.units}".
	// The expression language is the KPI one (numbers, + - * /, point references): no
	// functions, no loops. References must be metrics of this report.
	Computed []Computed `json:"computed,omitempty"`
	// Rollup adds a summary section grouped by "asset" or "site" (or nested "site>asset" / "asset>site") with a subtotal
	// per group and point and a grand total per point. Empty means off.
	Rollup string `json:"rollup,omitempty"`
	// Emissions adds a Scope 1 and 2 section (see emissions.go): metered quantity times an operator-entered factor.
	// Sections are titled plain-text notes printed after the data (see sections.go).
	Sections           []Section           `json:"sections,omitempty"`
	Emissions          []EmissionSource    `json:"emissions,omitempty"`
	EmissionsIntensity *EmissionsIntensity `json:"emissions_intensity,omitempty"`
	// Header and Footer are optional text (at most 80 printable characters) printed on every PDF
	// page, and at the top and bottom of the HTML. No markup; it is escaped.
	Header string `json:"header,omitempty"`
	Footer string `json:"footer,omitempty"`
	// Insights adds a statistical summary section (trend, peak, unusual buckets). Compare also sets each metric
	// against the window just before it (window up to 45 days). See insights.go.
	Insights bool `json:"insights,omitempty"`
	Compare  bool `json:"compare,omitempty"`
	// Chart draws each metric's averages as "line", "area" or "bar" inline SVG in the HTML report
	// (one chart per metric, or per metric column in the matrix layout). Empty means no chart.
	Chart string `json:"chart,omitempty"`
	// Highlight colours avg cells in the per-metric tables: red above Above, amber below Below.
	// Thresholds only (no expressions); matrix layout cells are not coloured yet.
	Highlight *Highlight `json:"highlight,omitempty"`
	// Detail turns on drill-through: under each metric table, every highlighted bucket (at most 10 per metric) gets a
	// collapsible list of its Detail highest raw readings (1 to 20). It needs a highlight rule or thresholds.
	Detail int `json:"detail,omitempty"`
	// Samples holds those raw readings per metric and bucket start (UTC unix seconds). Filled by the server at render
	// time, never stored or read from a request.
	Samples map[Metric]map[int64][]Sample `json:"-"`
	// Page sets the PDF page: "" or "a4" (portrait, default), "a4-landscape", "letter",
	// "letter-landscape". HTML and XLSX are unaffected.
	Page string `json:"page,omitempty"`
	// Theme is "" or "light" (white pages, default) or "dark" (dark pages with light text in HTML and PDF).
	Theme string `json:"theme,omitempty"`
	// Logo is the tenant's logo, filled by the server at render time and never stored in the definition.
	Logo image.Image `json:"-"`
	// Previous holds the preceding window's buckets for Compare. Filled by the server at render time.
	Previous map[Metric][]Bucket `json:"-"`
	// GroupLabels maps device_id to its asset or site name. Filled by the server
	// at render time from the database, never stored or read from a request.
	GroupLabels map[string]string `json:"-"`
	// InnerLabels is the same for the inner level of a nested rollup ("site>asset").
	InnerLabels map[string]string `json:"-"`
}

type Highlight struct {
	Above *float64 `json:"above,omitempty"`
	Below *float64 `json:"below,omitempty"`
	// When is an optional expression over one bucket: {row.avg} {row.min} {row.max} {row.sum} {row.count},
	// for example if({row.avg} > 50 and {row.max} < 100, 1, 0). A non-zero result colours the avg cell with
	// WhenColor ("red", default, or "amber"). It is checked before Above/Below.
	When      string `json:"when,omitempty"`
	WhenColor string `json:"when_color,omitempty"`
	expr      *kpi.Expr
}

var rowPoints = map[string]bool{"avg": true, "min": true, "max": true, "sum": true, "count": true}

func (h *Highlight) validateWhen() error {
	if h.When == "" {
		return nil
	}
	e, err := kpi.Parse(h.When)
	if err != nil {
		return fmt.Errorf("highlight when: %v", err)
	}
	for _, r := range e.Refs {
		if r.Device != "row" || !rowPoints[r.Point] {
			return fmt.Errorf("highlight when may only use {row.avg}, {row.min}, {row.max}, {row.sum}, {row.count}; got {%s}", r)
		}
	}
	if h.WhenColor != "" && h.WhenColor != "red" && h.WhenColor != "amber" {
		return fmt.Errorf("highlight when_color must be red or amber")
	}
	return nil
}

// kind returns "red", "amber" or "" for a bucket. Non-finite averages are never coloured.
func (h *Highlight) kind(b Bucket) string {
	if h == nil || math.IsNaN(b.Avg) || math.IsInf(b.Avg, 0) {
		return ""
	}
	if h.When != "" {
		if h.expr == nil {
			h.expr, _ = kpi.Parse(h.When) // validated before storing; nil means no match
		}
		if h.expr != nil {
			v, err := h.expr.Eval(map[string]float64{"row.avg": b.Avg, "row.min": b.Min, "row.max": b.Max, "row.sum": b.Sum, "row.count": float64(b.Count)})
			if err == nil && v != 0 {
				if h.WhenColor == "amber" {
					return "amber"
				}
				return "red"
			}
		}
	}
	if h.Above != nil && b.Avg > *h.Above {
		return "red"
	}
	if h.Below != nil && b.Avg < *h.Below {
		return "amber"
	}
	return ""
}

// Flagged reports whether a bucket is highlighted.
func (h *Highlight) Flagged(b Bucket) bool { return h.kind(b) != "" }

// fill is the docx/pptx shading for a bucket.
func (h *Highlight) fill(b Bucket) string {
	switch h.kind(b) {
	case "red":
		return "FECACA"
	case "amber":
		return "FDE68A"
	}
	return ""
}

func (h *Highlight) bucketStyle(b Bucket) string {
	switch h.kind(b) {
	case "red":
		return ` style="background:#fecaca;color:#7f1d1d"`
	case "amber":
		return ` style="background:#fde68a;color:#78350f"`
	}
	return ""
}

type Computed struct {
	Name string `json:"name"`
	Expr string `json:"expr"`
}

var (
	ErrNoMetrics  = errors.New("at least one metric required")
	ErrBadWindow  = errors.New("window_hours must be between 1 and 24*90")
	ErrBadGroupBy = errors.New("group_by must be 15min, hour, day or week")
	ErrBadLayout  = errors.New("layout must be empty or matrix; agg must be avg, min, max or sum")
	ErrBadRollup  = errors.New("rollup must be empty, asset, site, site>asset or asset>site")
	ErrBadID      = errors.New("device_id and point_id: lowercase letters, digits, - _ only")
)

func validID(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// Validate checks a definition before it is stored; ids are whitelisted
// characters only so they can never become SQL or HTML injection.
func Validate(d Definition) error {
	if len(d.Metrics) == 0 {
		return ErrNoMetrics
	}
	if len(d.Metrics) > 50 {
		return fmt.Errorf("at most 50 metrics per report")
	}
	for _, m := range d.Metrics {
		if !validID(m.DeviceID) || !validID(m.PointID) {
			return ErrBadID
		}
	}
	if d.WindowHours < 1 || d.WindowHours > 24*90 {
		return ErrBadWindow
	}
	if BucketExpr(d.GroupBy) == "" {
		return ErrBadGroupBy
	}
	if d.Theme != "" && d.Theme != "light" && d.Theme != "dark" {
		return fmt.Errorf("theme must be light or dark")
	}
	if h := d.Highlight; h != nil {
		for _, p := range []*float64{h.Above, h.Below} {
			if p != nil && (math.IsNaN(*p) || math.IsInf(*p, 0)) {
				return fmt.Errorf("highlight thresholds must be finite numbers")
			}
		}
		if h.Above != nil && h.Below != nil && *h.Below > *h.Above {
			return fmt.Errorf("highlight below must not exceed above")
		}
		if err := h.validateWhen(); err != nil {
			return err
		}
	}
	if _, ok := pageSizes[d.Page]; !ok {
		return fmt.Errorf("page must be empty, a4, a4-landscape, letter or letter-landscape")
	}
	if !validChart(d.Chart) {
		return fmt.Errorf("chart must be empty, line, area, bar, scatter, gauge or pie")
	}
	if d.Layout != "" && d.Layout != "matrix" {
		return ErrBadLayout
	}
	switch d.Agg {
	case "", "avg", "min", "max", "sum":
	default:
		return ErrBadLayout
	}
	if d.Detail != 0 {
		if d.Detail < 1 || d.Detail > 20 {
			return fmt.Errorf("detail must be between 1 and 20 readings")
		}
		if d.Highlight == nil || (d.Highlight.When == "" && d.Highlight.Above == nil && d.Highlight.Below == nil) {
			return fmt.Errorf("detail needs a highlight rule or threshold to decide which buckets to drill into")
		}
	}
	if d.Compare && (!d.Insights || d.WindowHours > 24*45) {
		return fmt.Errorf("compare needs insights on and a window of at most 45 days")
	}
	switch d.Rollup {
	case "", "asset", "site", "site>asset", "asset>site":
	default:
		return ErrBadRollup
	}
	if err := validateSections(d); err != nil {
		return err
	}
	if err := validateEmissions(d); err != nil {
		return err
	}
	for _, t := range []struct{ name, v string }{{"header", d.Header}, {"footer", d.Footer}} {
		if len(t.v) > 80 {
			return fmt.Errorf("%s must be at most 80 characters", t.name)
		}
		for _, r := range t.v {
			if r < 32 || r == 127 {
				return fmt.Errorf("%s must not contain control characters", t.name)
			}
		}
	}
	if len(d.Computed) > 0 {
		if d.Layout != "matrix" {
			return fmt.Errorf("computed columns need the matrix layout")
		}
		if len(d.Computed) > 5 {
			return fmt.Errorf("at most 5 computed columns")
		}
		have := map[string]bool{}
		for _, m := range d.Metrics {
			have[m.DeviceID+"."+m.PointID] = true
		}
		for _, c := range d.Computed {
			if n := strings.TrimSpace(c.Name); n == "" || len(n) > 64 {
				return fmt.Errorf("computed column name must be 1-64 characters")
			}
			e, err := kpi.Parse(c.Expr)
			if err != nil {
				return fmt.Errorf("computed %q: %v", c.Name, err)
			}
			for _, r := range e.Refs {
				if !have[r.String()] {
					return fmt.Errorf("computed %q refers to %s, which is not a metric of this report", c.Name, r)
				}
			}
		}
	}
	return nil
}

// BucketExpr returns the SQL expression that buckets observed_at for a
// group_by value, or "" when the value is not allowed. It only ever returns
// constants from this whitelist, so it is safe to splice into a query.
func BucketExpr(groupBy string) string {
	switch groupBy {
	case "15min":
		return "to_timestamp(floor(extract(epoch FROM observed_at) / 900) * 900)"
	case "hour":
		return "date_trunc('hour', observed_at)"
	case "day":
		return "date_trunc('day', observed_at)"
	case "week":
		return "date_trunc('week', observed_at)"
	}
	return ""
}

// Summary folds buckets into overall min/max, count-weighted average and sum.
func Summary(rows []Bucket) (avg, min, max, sum float64, n int) {
	for i, r := range rows {
		if i == 0 || r.Min < min {
			min = r.Min
		}
		if i == 0 || r.Max > max {
			max = r.Max
		}
		sum += r.Sum
		n += r.Count
	}
	if n > 0 {
		avg = sum / float64(n)
	}
	return
}

// Bucket is one aggregated cell: avg/min/max/count over one time bucket.
type Bucket struct {
	Start time.Time
	Avg   float64
	Min   float64
	Max   float64
	Sum   float64
	Count int
}

// Render produces a standalone HTML report. All dynamic text is escaped;
// numbers are formatted, never interpolated raw.
func Render(title string, d Definition, series map[Metric][]Bucket, generated time.Time) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><meta charset="utf-8"><title>` + html.EscapeString(title) + `</title>`)
	b.WriteString(`<style>body{font-family:system-ui,sans-serif;color:#111;max-width:900px;margin:24px auto;padding:0 16px}` +
		`h1{font-size:20px}h2{font-size:15px;margin-top:28px}table{border-collapse:collapse;width:100%}` +
		`td,th{border:1px solid #ddd;padding:6px 10px;text-align:right;font-size:13px}` +
		`th{background:#f5f5f5;text-align:left}td:first-child,th:first-child{text-align:left}` +
		`.meta{color:#666;font-size:12px}`)
	if d.Theme == "dark" {
		b.WriteString(`html{background:#16181d}body{color:#e8e8ea}td,th{border-color:#3a3d45}th{background:#22252c}.meta{color:#a0a3ab}`)
	}
	b.WriteString(`</style>`)
	if d.Logo != nil {
		var pb bytes.Buffer
		if png.Encode(&pb, d.Logo) == nil && pb.Len() < 400<<10 {
			w, h := logoSize(d.Logo)
			fmt.Fprintf(&b, `<img alt="" src="data:image/png;base64,%s" style="float:right;width:%.0fpx;height:%.0fpx">`, base64.StdEncoding.EncodeToString(pb.Bytes()), w*1.33, h*1.33)
		}
	}
	if d.Header != "" {
		b.WriteString(`<p class="meta">` + html.EscapeString(d.Header) + `</p>`)
	}
	b.WriteString(`<h1>` + html.EscapeString(title) + `</h1>`)
	fmt.Fprintf(&b, `<p class="meta">Generated %s - window %dh, grouped by %s</p>`,
		generated.UTC().Format(time.RFC3339), d.WindowHours, html.EscapeString(d.GroupBy))
	if d.Layout == "matrix" {
		hdr, rows, total := Matrix(d, series)
		writeChartsHTML(&b, d, series)
		b.WriteString(`<table><tr><th>` + html.EscapeString(d.GroupBy) + `</th>`)
		for _, h := range hdr {
			b.WriteString(`<th>` + html.EscapeString(h) + `</th>`)
		}
		b.WriteString(`</tr>`)
		for _, r := range rows {
			b.WriteString(`<tr>`)
			for _, c := range r {
				b.WriteString(`<td>` + html.EscapeString(c) + `</td>`)
			}
			b.WriteString(`</tr>`)
		}
		b.WriteString(`<tr style="font-weight:600;background:#fafafa">`)
		for _, c := range total {
			b.WriteString(`<td>` + html.EscapeString(c) + `</td>`)
		}
		b.WriteString(`</tr></table>`)
		writeInsightsHTML(&b, d, series)
		writeSectionsHTML(&b, d)
		writeEmissionsHTML(&b, d, series)
		writeRollupHTML(&b, d, series)
		writeFooter(&b, d)
		return b.String()
	}
	keys := make([]Metric, 0, len(series))
	for m := range series {
		keys = append(keys, m)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].DeviceID != keys[j].DeviceID {
			return keys[i].DeviceID < keys[j].DeviceID
		}
		return keys[i].PointID < keys[j].PointID
	})
	for _, m := range keys {
		b.WriteString(`<h2>` + html.EscapeString(m.DeviceID) + ` / ` + html.EscapeString(m.PointID) + `</h2>`)
		rows := series[m]
		if len(rows) == 0 {
			b.WriteString(`<p class="meta">no data in window</p>`)
			continue
		}
		if d.Chart != "" {
			b.WriteString(chartSVG(d.Chart, rows, d.Theme == "dark"))
		}
		b.WriteString(`<table><tr><th>` + html.EscapeString(d.GroupBy) + `</th><th>avg</th><th>min</th><th>max</th><th>sum</th><th>samples</th></tr>`)
		for _, r := range rows {
			fmt.Fprintf(&b, `<tr><td>%s</td><td%s>%.3f</td><td>%.3f</td><td>%.3f</td><td>%.3f</td><td>%d</td></tr>`,
				r.Start.UTC().Format("2006-01-02 15:04"), d.Highlight.bucketStyle(r), r.Avg, r.Min, r.Max, r.Sum, r.Count)
		}
		ta, tmin, tmax, tsum, tn := Summary(rows)
		fmt.Fprintf(&b, `<tr style="font-weight:600;background:#fafafa"><td>overall</td><td>%.3f</td><td>%.3f</td><td>%.3f</td><td>%.3f</td><td>%d</td></tr>`, ta, tmin, tmax, tsum, tn)
		b.WriteString(`</table>`)
		writeDetailHTML(&b, d, m, rows)
	}
	writeInsightsHTML(&b, d, series)
	writeSectionsHTML(&b, d)
	writeEmissionsHTML(&b, d, series)
	writeRollupHTML(&b, d, series)
	writeFooter(&b, d)
	return b.String()
}

// writeDetailHTML lists the raw readings behind each highlighted bucket, collapsed by default.
func writeDetailHTML(b *strings.Builder, d Definition, m Metric, rows []Bucket) {
	if d.Detail == 0 {
		return
	}
	shown := 0
	for _, r := range rows {
		smp := d.Samples[m][r.Start.Unix()]
		if d.Highlight.kind(r) == "" || len(smp) == 0 {
			continue
		}
		if shown == MaxDetailBuckets {
			b.WriteString(`<p class="meta">more highlighted buckets not shown (first ` + fmt.Sprint(MaxDetailBuckets) + ` per metric)</p>`)
			return
		}
		shown++
		fmt.Fprintf(b, `<details><summary>%s: top %d readings</summary><table><tr><th>time (UTC)</th><th>value</th></tr>`, r.Start.UTC().Format("2006-01-02 15:04"), len(smp))
		for _, x := range smp {
			fmt.Fprintf(b, `<tr><td>%s</td><td>%.3f</td></tr>`, x.At.UTC().Format("2006-01-02 15:04:05"), x.Value)
		}
		b.WriteString(`</table></details>`)
	}
}

func writeFooter(b *strings.Builder, d Definition) {
	if d.Footer != "" {
		b.WriteString(`<p class="meta" style="margin-top:28px">` + html.EscapeString(d.Footer) + `</p>`)
	}
}

func writeRollupHTML(b *strings.Builder, d Definition, series map[Metric][]Bucket) {
	rows := BuildRollup(d, series)
	if d.Rollup == "" {
		return
	}
	b.WriteString(`<h2>Summary by ` + html.EscapeString(RollupTitle(d)) + `</h2>`)
	if len(rows) == 0 {
		b.WriteString(`<p class="meta">no data in window</p>`)
		return
	}
	b.WriteString(`<table><tr>`)
	for _, h := range RollupHeader(d) {
		b.WriteString(`<th>` + html.EscapeString(h) + `</th>`)
	}
	b.WriteString(`</tr>`)
	for i, c := range RollupCells(rows) {
		style := ""
		if rows[i].Group == TotalLabel {
			style = ` style="font-weight:600;background:#fafafa"`
		} else if rows[i].Level == 1 {
			style = ` style="font-weight:600;background:#f3f4f6"`
		}
		b.WriteString(`<tr` + style + `>`)
		for _, v := range c {
			b.WriteString(`<td>` + html.EscapeString(v) + `</td>`)
		}
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</table><p class="meta">Each row totals one point across the devices of one group; points are never added to each other.</p>`)
}

// CSVSafe neutralizes spreadsheet formula injection: a cell starting with
// = + - @ tab or CR is prefixed with an apostrophe.
func CSVSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// RenderCSV flattens aggregated series to CSV (device, point, bucket, avg, min, max, count).
func RenderCSV(d Definition, series map[Metric][]Bucket) string {
	var b strings.Builder
	if d.Layout == "matrix" {
		hdr, rows, total := Matrix(d, series)
		b.WriteString(CSVSafe(d.GroupBy))
		for _, h := range hdr {
			b.WriteString("," + `"` + strings.ReplaceAll(CSVSafe(h), `"`, `""`) + `"`)
		}
		b.WriteString("\n")
		for _, r := range append(rows, total) {
			b.WriteString(strings.Join(r, ",") + "\n")
		}
		writeSectionsCSV(&b, d)
		writeEmissionsCSV(&b, d, series)
		writeRollupCSV(&b, d, series)
		return b.String()
	}
	b.WriteString("device_id,point_id,bucket_start,avg,min,max,count,sum\n")
	for _, m := range d.Metrics {
		for _, k := range series[m] {
			fmt.Fprintf(&b, "%s,%s,%s,%.6g,%.6g,%.6g,%d,%.6g\n", CSVSafe(m.DeviceID), CSVSafe(m.PointID),
				k.Start.UTC().Format(time.RFC3339), k.Avg, k.Min, k.Max, k.Count, k.Sum)
		}
	}
	writeSectionsCSV(&b, d)
	writeEmissionsCSV(&b, d, series)
	writeRollupCSV(&b, d, series)
	return b.String()
}

func writeRollupCSV(b *strings.Builder, d Definition, series map[Metric][]Bucket) {
	if d.Rollup == "" {
		return
	}
	b.WriteString("\n")
	q := func(s string) string { return `"` + strings.ReplaceAll(CSVSafe(s), `"`, `""`) + `"` }
	cells := []string{}
	for _, h := range RollupHeader(d) {
		cells = append(cells, q(h))
	}
	b.WriteString(strings.Join(cells, ",") + "\n")
	for _, r := range RollupCells(BuildRollup(d, series)) {
		cells = cells[:0]
		for _, v := range r {
			cells = append(cells, q(v))
		}
		b.WriteString(strings.Join(cells, ",") + "\n")
	}
}

func aggOf(d Definition, b Bucket) float64 {
	switch d.Agg {
	case "min":
		return b.Min
	case "max":
		return b.Max
	case "sum":
		return b.Sum
	}
	return b.Avg
}

// Matrix pivots the series into one row per time bucket and one column per
// metric ("device / point"). A cell with no data is "-". The last row is the
// per-column total using the same aggregate (count-weighted average, min of
// mins, max of maxes, or sum). Rows are returned as strings, ready for any
// renderer, with the bucket label as the first cell.
func Matrix(d Definition, series map[Metric][]Bucket) (header []string, rows [][]string, total []string) {
	times := map[time.Time]bool{}
	for _, m := range d.Metrics {
		header = append(header, m.DeviceID+" / "+m.PointID)
		for _, b := range series[m] {
			times[b.Start.UTC()] = true
		}
	}
	var ts []time.Time
	for t := range times {
		ts = append(ts, t)
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].Before(ts[j]) })
	idx := make([]map[time.Time]Bucket, len(d.Metrics))
	for i, m := range d.Metrics {
		idx[i] = map[time.Time]Bucket{}
		for _, b := range series[m] {
			idx[i][b.Start.UTC()] = b
		}
	}
	exprs := make([]*kpi.Expr, len(d.Computed))
	for i, c := range d.Computed {
		header = append(header, c.Name)
		exprs[i], _ = kpi.Parse(c.Expr) // validated by Validate; nil means no column value
	}
	compute := func(vals map[string]float64) []string {
		out := make([]string, len(exprs))
		for i, e := range exprs {
			out[i] = "-"
			if e == nil {
				continue
			}
			if v, err := e.Eval(vals); err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) {
				out[i] = fmt.Sprintf("%.3f", v)
			}
		}
		return out
	}
	for _, t := range ts {
		r := []string{t.Format("2006-01-02 15:04")}
		vals := map[string]float64{}
		for i, m := range d.Metrics {
			if b, ok := idx[i][t]; ok {
				v := aggOf(d, b)
				vals[m.DeviceID+"."+m.PointID] = v
				r = append(r, fmt.Sprintf("%.3f", v))
			} else {
				r = append(r, "-")
			}
		}
		rows = append(rows, append(r, compute(vals)...))
	}
	total = []string{"overall"}
	tvals := map[string]float64{}
	for _, m := range d.Metrics {
		a, mn, mx, sm, n := Summary(series[m])
		if n == 0 {
			total = append(total, "-")
			continue
		}
		v := a
		switch d.Agg {
		case "min":
			v = mn
		case "max":
			v = mx
		case "sum":
			v = sm
		}
		tvals[m.DeviceID+"."+m.PointID] = v
		total = append(total, fmt.Sprintf("%.3f", v))
	}
	total = append(total, compute(tvals)...)
	return
}

// ApplyParams overrides a stored definition at run time from query values
// (window_hours, group_by, layout, agg, device). These are the report's parameters:
// the stored definition stays unchanged and the result is validated again.
func ApplyParams(d Definition, get func(string) string) (Definition, error) {
	if v := get("device"); v != "" {
		// Run the same report for other devices. "device=a" swaps the device; "device=a,b,c"
		// (multivalue, at most 10) repeats every point for each listed device. Only when every
		// metric is on one device (so the swap is unambiguous) and there are no computed columns,
		// which name devices.
		var devs []string
		seen := map[string]bool{}
		for _, id := range strings.Split(v, ",") {
			if !validID(id) {
				return d, ErrBadID
			}
			if !seen[id] {
				seen[id] = true
				devs = append(devs, id)
			}
		}
		if len(devs) > 10 {
			return d, errors.New("the device parameter takes at most 10 devices")
		}
		if len(d.Computed) > 0 {
			return d, errors.New("the device parameter cannot be used with computed columns")
		}
		for _, m := range d.Metrics {
			if m.DeviceID != d.Metrics[0].DeviceID {
				return d, errors.New("the device parameter needs a report whose metrics are all on one device")
			}
		}
		var ms []Metric
		for _, dev := range devs {
			for _, m := range d.Metrics {
				ms = append(ms, Metric{DeviceID: dev, PointID: m.PointID})
			}
		}
		d.Metrics = ms
	}
	if v := get("window_hours"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return d, ErrBadWindow
		}
		d.WindowHours = n
	}
	if v := get("group_by"); v != "" {
		d.GroupBy = v
	}
	if v := get("layout"); v != "" {
		if v == "flat" {
			v = ""
		}
		d.Layout = v
	}
	if v := get("agg"); v != "" {
		d.Agg = v
	}
	if v := get("page"); v != "" {
		d.Page = v
	}
	return d, Validate(d)
}

// writeChartsHTML adds one chart per metric under the matrix table.
func writeChartsHTML(b *strings.Builder, d Definition, series map[Metric][]Bucket) {
	if d.Chart == "" {
		return
	}
	keys := make([]Metric, 0, len(series))
	for m := range series {
		keys = append(keys, m)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].DeviceID != keys[j].DeviceID {
			return keys[i].DeviceID < keys[j].DeviceID
		}
		return keys[i].PointID < keys[j].PointID
	})
	for _, m := range keys {
		b.WriteString(`<h2>` + html.EscapeString(m.DeviceID) + ` / ` + html.EscapeString(m.PointID) + `</h2>`)
		b.WriteString(chartSVG(d.Chart, series[m], d.Theme == "dark"))
	}
}

var pageSizes = map[string][2]int{
	"": {595, 842}, "a4": {595, 842}, "a4-landscape": {842, 595},
	"letter": {612, 792}, "letter-landscape": {792, 612},
}

// PageSize returns the PDF page width and height in points; unknown names get A4 portrait.
func PageSize(p string) (int, int) {
	if v, ok := pageSizes[p]; ok {
		return v[0], v[1]
	}
	return 595, 842
}
