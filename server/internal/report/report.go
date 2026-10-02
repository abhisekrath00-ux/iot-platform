// Package report validates report definitions and renders telemetry
// aggregates into a self-contained HTML document (inline CSS, no external
// assets: it must render in air-gapped mail clients and browsers).
package report

import (
	"errors"
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/kpi"
	"html"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

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
	// Rollup adds a summary section grouped by "asset" or "site" with a subtotal
	// per group and point and a grand total per point. Empty means off.
	Rollup string `json:"rollup,omitempty"`
	// GroupLabels maps device_id to its asset or site name. Filled by the server
	// at render time from the database, never stored or read from a request.
	GroupLabels map[string]string `json:"-"`
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
	ErrBadRollup  = errors.New("rollup must be empty, asset or site")
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
	if d.Layout != "" && d.Layout != "matrix" {
		return ErrBadLayout
	}
	switch d.Agg {
	case "", "avg", "min", "max", "sum":
	default:
		return ErrBadLayout
	}
	switch d.Rollup {
	case "", "asset", "site":
	default:
		return ErrBadRollup
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
		`.meta{color:#666;font-size:12px}</style>`)
	b.WriteString(`<h1>` + html.EscapeString(title) + `</h1>`)
	fmt.Fprintf(&b, `<p class="meta">Generated %s - window %dh, grouped by %s</p>`,
		generated.UTC().Format(time.RFC3339), d.WindowHours, html.EscapeString(d.GroupBy))
	if d.Layout == "matrix" {
		hdr, rows, total := Matrix(d, series)
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
		writeRollupHTML(&b, d, series)
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
		b.WriteString(`<table><tr><th>` + html.EscapeString(d.GroupBy) + `</th><th>avg</th><th>min</th><th>max</th><th>sum</th><th>samples</th></tr>`)
		for _, r := range rows {
			fmt.Fprintf(&b, `<tr><td>%s</td><td>%.3f</td><td>%.3f</td><td>%.3f</td><td>%.3f</td><td>%d</td></tr>`,
				r.Start.UTC().Format("2006-01-02 15:04"), r.Avg, r.Min, r.Max, r.Sum, r.Count)
		}
		ta, tmin, tmax, tsum, tn := Summary(rows)
		fmt.Fprintf(&b, `<tr style="font-weight:600;background:#fafafa"><td>overall</td><td>%.3f</td><td>%.3f</td><td>%.3f</td><td>%.3f</td><td>%d</td></tr>`, ta, tmin, tmax, tsum, tn)
		b.WriteString(`</table>`)
	}
	writeRollupHTML(&b, d, series)
	return b.String()
}

func writeRollupHTML(b *strings.Builder, d Definition, series map[Metric][]Bucket) {
	rows := BuildRollup(d, series)
	if d.Rollup == "" {
		return
	}
	b.WriteString(`<h2>Summary by ` + html.EscapeString(d.Rollup) + `</h2>`)
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
// (window_hours, group_by, layout, agg). These are the report's parameters:
// the stored definition stays unchanged and the result is validated again.
func ApplyParams(d Definition, get func(string) string) (Definition, error) {
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
	return d, Validate(d)
}
