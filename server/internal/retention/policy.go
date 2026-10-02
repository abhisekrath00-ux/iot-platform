package retention

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RollupDaily rebuilds whole-UTC-day buckets in [from, to) from hourly rows.
// It never reads raw telemetry, so it is cheap and safe to re-run.
func RollupDaily(ctx context.Context, pool *pgxpool.Pool, from, to time.Time) (int64, error) {
	from, to = from.UTC().Truncate(24*time.Hour), to.UTC().Truncate(24*time.Hour)
	if !to.After(from) {
		return 0, nil
	}
	ct, err := pool.Exec(ctx, `
		INSERT INTO telemetry_rollup_daily(tenant_id, device_id, point_id, bucket, n, sum, min, max)
		SELECT tenant_id, device_id, point_id, date_trunc('day', bucket AT TIME ZONE 'UTC') AT TIME ZONE 'UTC',
		       sum(n), sum(sum), min(min), max(max)
		FROM telemetry_rollup_hourly
		WHERE bucket >= $1 AND bucket < $2
		GROUP BY 1,2,3,4
		ON CONFLICT (tenant_id, device_id, point_id, bucket)
		DO UPDATE SET n=EXCLUDED.n, sum=EXCLUDED.sum, min=EXCLUDED.min, max=EXCLUDED.max`, from, to)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// PurgeHourly deletes hourly rollups older than cutoff (truncated to a whole
// day) for one tenant, after making sure the daily rollup covers them. If the
// daily rollup fails nothing is deleted.
func PurgeHourly(ctx context.Context, pool *pgxpool.Pool, tenant string, cutoff time.Time) (int64, error) {
	cutoff = cutoff.UTC().Truncate(24 * time.Hour)
	var oldest *time.Time
	if err := pool.QueryRow(ctx, `SELECT min(bucket) FROM telemetry_rollup_hourly WHERE tenant_id=$1 AND bucket < $2`, tenant, cutoff).Scan(&oldest); err != nil {
		return 0, err
	}
	if oldest == nil {
		return 0, nil
	}
	if _, err := RollupDaily(ctx, pool, *oldest, cutoff); err != nil {
		return 0, fmt.Errorf("daily rollup before purge: %w", err)
	}
	ct, err := pool.Exec(ctx, `DELETE FROM telemetry_rollup_hourly WHERE tenant_id=$1 AND bucket < $2`, tenant, cutoff)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// PurgeRawTenant is Purge limited to one tenant (rollups first, then batched deletes).
func PurgeRawTenant(ctx context.Context, pool *pgxpool.Pool, tenant string, cutoff time.Time) (int64, error) {
	cutoff = cutoff.UTC().Truncate(time.Hour)
	var oldest *time.Time
	if err := pool.QueryRow(ctx, `SELECT min(observed_at) FROM telemetry WHERE tenant_id=$1 AND observed_at < $2`, tenant, cutoff).Scan(&oldest); err != nil {
		return 0, err
	}
	if oldest == nil {
		return 0, nil
	}
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
			  SELECT event_id, observed_at FROM telemetry WHERE tenant_id=$1 AND observed_at < $2 LIMIT 5000) d
			WHERE t.event_id = d.event_id AND t.observed_at = d.observed_at`, tenant, cutoff)
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

// ApplyTenantPolicies enforces every tenant_retention row. Tenants without a
// row are untouched here; the global RAW_RETENTION_DAYS purge skips tenants
// that set their own raw_days (see Purge's exclusion).
func ApplyTenantPolicies(ctx context.Context, pool *pgxpool.Pool, now time.Time) {
	rows, err := pool.Query(ctx, `SELECT tenant_id, raw_days, hourly_days FROM tenant_retention`)
	if err != nil {
		log.Printf("retention: policies: %v", err)
		return
	}
	type pol struct {
		t      string
		raw    *int
		hourly *int
	}
	var ps []pol
	for rows.Next() {
		var p pol
		if err := rows.Scan(&p.t, &p.raw, &p.hourly); err == nil {
			ps = append(ps, p)
		}
	}
	rows.Close()
	for _, p := range ps {
		if p.raw != nil {
			if n, err := PurgeRawTenant(ctx, pool, p.t, now.Add(-time.Duration(*p.raw)*24*time.Hour)); err != nil {
				log.Printf("retention: tenant %s raw purge: %v", p.t, err)
			} else if n > 0 {
				log.Printf("retention: tenant %s purged %d raw rows older than %d days", p.t, n, *p.raw)
			}
		}
		if p.hourly != nil {
			if n, err := PurgeHourly(ctx, pool, p.t, now.Add(-time.Duration(*p.hourly)*24*time.Hour)); err != nil {
				log.Printf("retention: tenant %s hourly purge: %v", p.t, err)
			} else if n > 0 {
				log.Printf("retention: tenant %s purged %d hourly rollups older than %d days (daily kept)", p.t, n, *p.hourly)
			}
		}
	}
}
