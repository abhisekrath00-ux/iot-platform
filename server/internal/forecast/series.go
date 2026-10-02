package forecast

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// HourlySeries returns one average per UTC hour for the last `hours` complete
// hours, reading hourly rollups plus raw rows for hours not rolled up yet. Missing
// hours are linearly interpolated; if more than 20% are missing ok=false, since a
// forecast over mostly invented data would be a false claim.
func HourlySeries(ctx context.Context, pool *pgxpool.Pool, tenant, dev, pt string, hours int) (start time.Time, vals []float64, ok bool, err error) {
	end := time.Now().UTC().Truncate(time.Hour)
	start = end.Add(-time.Duration(hours) * time.Hour)
	rows, err := pool.Query(ctx, `
		WITH parts AS (
		  SELECT bucket AS h, sum AS s, n FROM telemetry_rollup_hourly
		   WHERE tenant_id=$1 AND device_id=$2 AND point_id=$3 AND bucket >= $4 AND bucket < $5
		  UNION ALL
		  SELECT date_trunc('hour', observed_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC', value, 1 FROM telemetry
		   WHERE tenant_id=$1 AND device_id=$2 AND point_id=$3 AND quality IN ('measured','estimated')
		     AND observed_at >= $4 AND observed_at < $5
		     AND NOT EXISTS (SELECT 1 FROM telemetry_rollup_hourly r WHERE r.tenant_id=$1 AND r.device_id=$2 AND r.point_id=$3
		                      AND r.bucket = date_trunc('hour', observed_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'))
		SELECT h, sum(s)/sum(n) FROM parts GROUP BY h ORDER BY h`, tenant, dev, pt, start, end)
	if err != nil {
		return start, nil, false, err
	}
	defer rows.Close()
	have := make([]*float64, hours)
	got := 0
	for rows.Next() {
		var h time.Time
		var v float64
		if rows.Scan(&h, &v) != nil {
			continue
		}
		i := int(h.UTC().Sub(start) / time.Hour)
		if i >= 0 && i < hours {
			x := v
			have[i] = &x
			got++
		}
	}
	if got < hours*8/10 || got < 2 {
		return start, nil, false, nil
	}
	vals = make([]float64, hours)
	prev := -1
	for i := 0; i < hours; i++ {
		if have[i] == nil {
			continue
		}
		vals[i] = *have[i]
		if prev >= 0 && i-prev > 1 {
			for j := prev + 1; j < i; j++ {
				vals[j] = vals[prev] + (vals[i]-vals[prev])*float64(j-prev)/float64(i-prev)
			}
		}
		if prev < 0 {
			for j := 0; j < i; j++ {
				vals[j] = vals[i]
			}
		}
		prev = i
	}
	for j := prev + 1; j < hours; j++ {
		vals[j] = vals[prev]
	}
	return start, vals, true, nil
}
