// Package retention rolls raw telemetry up into hourly aggregates and, when a
// retention window is configured, deletes raw rows that are already covered
// by a rollup. Rollups are recomputed from raw data for whole hours, so the
// job is idempotent and safe to re-run.
package retention

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Rollup aggregates raw rows with observed_at in [from, to) into hourly
// buckets. Both bounds are truncated to whole hours (from down, to down) so
// only complete hours are written. Only measured/estimated samples count.
func Rollup(ctx context.Context, pool *pgxpool.Pool, from, to time.Time) (int64, error) {
	from, to = from.UTC().Truncate(time.Hour), to.UTC().Truncate(time.Hour)
	if !to.After(from) {
		return 0, nil
	}
	ct, err := pool.Exec(ctx, `
		INSERT INTO telemetry_rollup_hourly(tenant_id, device_id, point_id, bucket, n, sum, min, max)
		SELECT tenant_id, device_id, point_id, date_trunc('hour', observed_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC',
		       count(*), sum(value), min(value), max(value)
		FROM telemetry
		WHERE observed_at >= $1 AND observed_at < $2 AND quality IN ('measured','estimated')
		GROUP BY 1,2,3,4
		ON CONFLICT (tenant_id, device_id, point_id, bucket)
		DO UPDATE SET n=EXCLUDED.n, sum=EXCLUDED.sum, min=EXCLUDED.min, max=EXCLUDED.max`, from, to)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// Purge rolls up everything older than cutoff (whole hours), then deletes the
// raw rows older than that hour in batches. It returns rows deleted. If the
// rollup fails nothing is deleted.
func Purge(ctx context.Context, pool *pgxpool.Pool, cutoff time.Time) (int64, error) {
	cutoff = cutoff.UTC().Truncate(time.Hour)
	var oldest *time.Time
	if err := pool.QueryRow(ctx, `SELECT min(observed_at) FROM telemetry WHERE observed_at < $1`, cutoff).Scan(&oldest); err != nil {
		return 0, err
	}
	if oldest == nil {
		return 0, nil
	}
	// Roll up in day-sized steps so a first run over a large backlog stays bounded.
	for from := oldest.UTC().Truncate(time.Hour); from.Before(cutoff); from = from.Add(24 * time.Hour) {
		to := from.Add(24 * time.Hour)
		if to.After(cutoff) {
			to = cutoff
		}
		if _, err := Rollup(ctx, pool, from, to); err != nil {
			return 0, fmt.Errorf("rollup before purge: %w", err)
		}
	}
	var total int64
	for ctx.Err() == nil {
		ct, err := pool.Exec(ctx, `
			DELETE FROM telemetry t USING (
			  SELECT event_id, observed_at FROM telemetry WHERE observed_at < $1 LIMIT 5000) d
			WHERE t.event_id = d.event_id AND t.observed_at = d.observed_at`, cutoff)
		if err != nil {
			return total, err
		}
		total += ct.RowsAffected()
		if ct.RowsAffected() == 0 {
			break
		}
	}
	return total, nil
}

// Job is the leader-elected loop: hourly it rolls up the last 48 hours and,
// if retentionDays > 0, purges raw rows older than that.
func Job(pool *pgxpool.Pool, retentionDays int, every time.Duration) func(context.Context) {
	return func(ctx context.Context) {
		run := func() {
			now := time.Now()
			if n, err := Rollup(ctx, pool, now.Add(-48*time.Hour), now); err != nil {
				log.Printf("retention: rollup: %v", err)
			} else {
				log.Printf("retention: rolled up %d buckets", n)
			}
			if retentionDays > 0 {
				n, err := Purge(ctx, pool, now.Add(-time.Duration(retentionDays)*24*time.Hour))
				if err != nil {
					log.Printf("retention: purge: %v", err)
				} else if n > 0 {
					log.Printf("retention: purged %d raw rows older than %d days (rollups kept)", n, retentionDays)
				}
			}
		}
		run()
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				run()
			}
		}
	}
}
