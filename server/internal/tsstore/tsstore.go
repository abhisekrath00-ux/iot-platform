// Package tsstore is the read seam between analytics code and the time-series
// backend. Reports read through Store, so a different engine (Timescale,
// ClickHouse) can be added as another implementation without touching report
// code. Only the Postgres implementation exists; docs/timeseries-store.md says
// why no other engine is built yet. Any new implementation must pass
// storetest.Run before it is used.
package tsstore

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/report"
)

// SeriesQuery asks for one point's aggregated buckets over a trailing window.
type SeriesQuery struct {
	Tenant      string
	DeviceID    string
	PointID     string
	WindowHours int
	GroupBy     string // 15min | hour | day | week
}

// Store reads aggregated telemetry. Implementations must scope every read to
// Tenant, return buckets ordered by Start, and treat a window with no data as
// an empty result, not an error.
type Store interface {
	Aggregate(ctx context.Context, q SeriesQuery) ([]report.Bucket, error)
}

// Postgres reads raw telemetry plus the hourly and daily rollups, so windows
// that reach past raw retention still return data for hour and coarser groups.
type Postgres struct{ Pool *pgxpool.Pool }

func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{Pool: pool} }

func (p *Postgres) Aggregate(ctx context.Context, q SeriesQuery) ([]report.Bucket, error) {
	bucketExpr := report.BucketExpr(q.GroupBy) // whitelisted constant
	if bucketExpr == "" {
		return nil, report.ErrBadGroupBy
	}
	window := itoa(q.WindowHours)
	// Hour-or-coarser buckets read rollups for the part of the window whose raw
	// samples were purged, and raw rows from the first raw hour onward.
	// 15min buckets cannot come from hourly rollups and read raw only.
	boundary := time.Now().Add(-time.Duration(q.WindowHours+1) * time.Hour)
	useRollup := q.GroupBy != "15min"
	if useRollup {
		var first *time.Time
		if err := p.Pool.QueryRow(ctx,
			`SELECT min(observed_at) FROM telemetry WHERE tenant_id=$1 AND device_id=$2 AND point_id=$3
			   AND observed_at > now() - ($4 || ' hours')::interval`,
			q.Tenant, q.DeviceID, q.PointID, window).Scan(&first); err != nil {
			return nil, err
		}
		if first == nil {
			boundary = time.Now().Add(time.Hour) // no raw in window: rollups only
		} else {
			boundary = first.UTC().Truncate(time.Hour)
		}
	}
	rows, err := p.Pool.Query(ctx,
		`WITH parts AS (
		   SELECT observed_at, 1::bigint AS n, value AS s, value AS mn, value AS mx
		   FROM telemetry
		   WHERE tenant_id=$1 AND device_id=$2 AND point_id=$3
		     AND observed_at > now() - ($4 || ' hours')::interval AND observed_at >= $5
		   UNION ALL
		   SELECT bucket, n, sum, min, max FROM telemetry_rollup_hourly
		   WHERE $6 AND tenant_id=$1 AND device_id=$2 AND point_id=$3
		     AND bucket >= date_trunc('hour', now() - ($4 || ' hours')::interval) AND bucket < $5
		   UNION ALL
		   SELECT bucket, n, sum, min, max FROM telemetry_rollup_daily
		   WHERE $7 AND tenant_id=$1 AND device_id=$2 AND point_id=$3
		     AND bucket >= date_trunc('day', now() - ($4 || ' hours')::interval)
		     AND bucket < date_trunc('day', COALESCE((SELECT min(bucket) FROM telemetry_rollup_hourly
		         WHERE tenant_id=$1 AND device_id=$2 AND point_id=$3
		           AND bucket >= date_trunc('hour', now() - ($4 || ' hours')::interval)), $5)))
		 SELECT `+bucketExpr+` AS bucket,
		        sum(s)/sum(n), min(mn), max(mx), sum(s), sum(n)
		 FROM parts GROUP BY bucket ORDER BY bucket`,
		q.Tenant, q.DeviceID, q.PointID, window, boundary, useRollup, q.GroupBy == "day" || q.GroupBy == "week")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []report.Bucket
	for rows.Next() {
		var b report.Bucket
		var n int64
		if err := rows.Scan(&b.Start, &b.Avg, &b.Min, &b.Max, &b.Sum, &n); err != nil {
			return nil, err
		}
		b.Count = int(n)
		out = append(out, b)
	}
	return out, rows.Err()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
