package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/report"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/tsstore"
)

// withReportContext fills what a render needs beyond the stored definition: rollup group labels and, for a
// compare report, the buckets of the window just before the current one.
func (s *server) withReportContext(ctx context.Context, tenant string, def report.Definition) (report.Definition, error) {
	def, err := s.withGroupLabels(ctx, tenant, def)
	def.Logo = s.reportLogoImage(ctx, tenant)
	if err != nil {
		return def, err
	}
	def = s.subSummaries(ctx, tenant, def)
	if def.Detail > 0 {
		if def.Samples, err = s.detailSamples(ctx, tenant, def); err != nil {
			return def, err
		}
	}
	if !def.Compare {
		return def, nil
	}
	store := s.ts
	if store == nil {
		store = tsstore.NewPostgres(s.st.Pool)
	}
	dur := map[string]time.Duration{"15min": 15 * time.Minute, "hour": time.Hour, "day": 24 * time.Hour, "week": 7 * 24 * time.Hour}[def.GroupBy]
	cutoff := time.Now().Add(-time.Duration(def.WindowHours) * time.Hour)
	def.Previous = map[report.Metric][]report.Bucket{}
	for _, m := range def.Metrics {
		bs, err := store.Aggregate(ctx, tsstore.SeriesQuery{Tenant: tenant, DeviceID: m.DeviceID, PointID: m.PointID, WindowHours: def.WindowHours * 2, GroupBy: def.GroupBy})
		if err != nil {
			return def, err
		}
		for _, b := range bs {
			if !b.Start.Add(dur).After(cutoff) { // wholly before the current window
				def.Previous[m] = append(def.Previous[m], b)
			}
		}
	}
	return def, nil
}

// detailSamples reads the highest raw readings of every highlighted bucket, for drill-through. Opt-in, tenant-scoped,
// at most MaxDetailBuckets buckets per metric and 20 readings per bucket.
func (s *server) detailSamples(ctx context.Context, tenant string, def report.Definition) (map[report.Metric]map[int64][]report.Sample, error) {
	store := s.ts
	if store == nil {
		store = tsstore.NewPostgres(s.st.Pool)
	}
	dur := map[string]time.Duration{"15min": 15 * time.Minute, "hour": time.Hour, "day": 24 * time.Hour, "week": 7 * 24 * time.Hour}[def.GroupBy]
	out := map[report.Metric]map[int64][]report.Sample{}
	for _, m := range def.Metrics {
		bs, err := store.Aggregate(ctx, tsstore.SeriesQuery{Tenant: tenant, DeviceID: m.DeviceID, PointID: m.PointID, WindowHours: def.WindowHours, GroupBy: def.GroupBy})
		if err != nil {
			return nil, err
		}
		n := 0
		for _, b := range bs {
			if def.Highlight.Flagged(b) {
				if n == report.MaxDetailBuckets {
					break
				}
				n++
				rows, err := s.st.Pool.Query(ctx,
					`SELECT observed_at, value FROM telemetry WHERE tenant_id=$1 AND device_id=$2 AND point_id=$3
					   AND observed_at >= $4 AND observed_at < $5 ORDER BY value DESC, observed_at LIMIT $6`,
					tenant, m.DeviceID, m.PointID, b.Start, b.Start.Add(dur), def.Detail)
				if err != nil {
					return nil, err
				}
				for rows.Next() {
					var x report.Sample
					if err := rows.Scan(&x.At, &x.Value); err != nil {
						rows.Close()
						return nil, err
					}
					if out[m] == nil {
						out[m] = map[int64][]report.Sample{}
					}
					out[m][b.Start.Unix()] = append(out[m][b.Start.Unix()], x)
				}
				rows.Close()
			}
		}
	}
	return out, nil
}

// subSummaries fills the embedded summaries of other saved reports of the same workspace. Tenant-scoped by the
// query; a missing report becomes a note, not an error; the embedded report's own subreports are ignored so there
// is no recursion. Plain text only.
func (s *server) subSummaries(ctx context.Context, tenant string, def report.Definition) report.Definition {
	def.SubSummaries = nil
	for _, ref := range def.Subreports {
		var name string
		var raw []byte
		err := s.st.Pool.QueryRow(ctx, `SELECT name, definition FROM reports WHERE id=$1 AND tenant_id=$2`, ref.ReportID, tenant).Scan(&name, &raw)
		var sub report.Definition
		if err == nil {
			err = json.Unmarshal(raw, &sub)
		}
		if err != nil {
			def.SubSummaries = append(def.SubSummaries, report.Section{Title: "Subreport", Body: "This report is not available (deleted, or not in this workspace)."})
			continue
		}
		sub.Subreports, sub.Sections = nil, nil
		series, _, serr := s.buildSeries(ctx, tenant, sub)
		title := "Subreport: " + name
		if r := []rune(title); len(r) > 80 {
			title = string(r[:80])
		}
		var lines []string
		if serr != nil {
			lines = append(lines, "The data for this report could not be read.")
		}
		for _, m := range sub.Metrics {
			avg, mn, mx, sum, n := report.Summary(series[m])
			if n == 0 {
				lines = append(lines, fmt.Sprintf("%s.%s: no readings in the last %d h", m.DeviceID, m.PointID, sub.WindowHours))
				continue
			}
			lines = append(lines, fmt.Sprintf("%s.%s: avg %.4g, min %.4g, max %.4g, sum %.4g (%d readings, last %d h)", m.DeviceID, m.PointID, avg, mn, mx, sum, n, sub.WindowHours))
		}
		if len(lines) == 0 {
			lines = append(lines, "This report has no metrics.")
		}
		def.SubSummaries = append(def.SubSummaries, report.Section{Title: title, Body: strings.Join(lines, "\n")})
	}
	return def
}
