// Package report validates report definitions and renders telemetry
// aggregates into a self-contained HTML document (inline CSS, no external
// assets: it must render in air-gapped mail clients and browsers).
package report

import (
	"errors"
	"fmt"
	"html"
	"sort"
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
	GroupBy     string   `json:"group_by"` // 15min|hour|day|week
}

var (
	ErrNoMetrics  = errors.New("at least one metric required")
	ErrBadWindow  = errors.New("window_hours must be between 1 and 24*90")
	ErrBadGroupBy = errors.New("group_by must be 15min, hour, day or week")
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
	return b.String()
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
	b.WriteString("device_id,point_id,bucket_start,avg,min,max,count,sum\n")
	for _, m := range d.Metrics {
		for _, k := range series[m] {
			fmt.Fprintf(&b, "%s,%s,%s,%.6g,%.6g,%.6g,%d,%.6g\n", CSVSafe(m.DeviceID), CSVSafe(m.PointID),
				k.Start.UTC().Format(time.RFC3339), k.Avg, k.Min, k.Max, k.Count, k.Sum)
		}
	}
	return b.String()
}
