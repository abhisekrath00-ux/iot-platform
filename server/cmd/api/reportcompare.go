package main

import (
	"context"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/report"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/tsstore"
)

// withReportContext fills what a render needs beyond the stored definition: rollup group labels and, for a
// compare report, the buckets of the window just before the current one.
func (s *server) withReportContext(ctx context.Context, tenant string, def report.Definition) (report.Definition, error) {
	def, err := s.withGroupLabels(ctx, tenant, def)
	if err != nil || !def.Compare {
		return def, err
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
