package store

import (
	"context"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RecordTrail records sampled positions. Callers treat failure as best-effort
// metadata, never as a failed position update. Serialize per device so concurrent
// processes share the same distance/heartbeat decision. Inactive rows are not
// swept here: pruning runs on the next record attempt for that device.
func RecordTrail(ctx context.Context, pool *pgxpool.Pool, tenant, device string, lat, lon float64, at time.Time, source string) error {
	if !validAxis(lat, 90) || !validAxis(lon, 180) || at.Before(time.Now().Add(-7*24*time.Hour)) {
		return nil
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var id string
	if err = tx.QueryRow(ctx, `SELECT id FROM devices WHERE id=$1 AND tenant_id=$2 FOR UPDATE`, device, tenant).Scan(&id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM device_positions WHERE device_id=$1 AND recorded_at < now()-interval '7 days'`, device); err != nil {
		return err
	}
	var lastLat, lastLon float64
	var lastAt time.Time
	err = tx.QueryRow(ctx, `SELECT lat,lon,recorded_at FROM device_positions WHERE device_id=$1 ORDER BY recorded_at DESC LIMIT 1`, device).Scan(&lastLat, &lastLon, &lastAt)
	if err != nil && err != pgx.ErrNoRows {
		return err
	}
	// Historical fixes are retained, not suppressed by a newer arrival. At a
	// duplicate timestamp the first recorded fix wins.
	keep := err == pgx.ErrNoRows || at.Before(lastAt) || at.Sub(lastAt) >= 5*time.Minute || trailDistM(lastLat, lastLon, lat, lon) >= 5
	if keep {
		_, err = tx.Exec(ctx, `INSERT INTO device_positions(tenant_id,device_id,lat,lon,recorded_at,source) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, tenant, device, lat, lon, at, source)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func trailDistM(a, b, c, d float64) float64 {
	p1, p2 := a*math.Pi/180, c*math.Pi/180
	dp, dl := (c-a)*math.Pi/180, (d-b)*math.Pi/180
	v := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * 6371000 * math.Asin(math.Sqrt(math.Min(1, v)))
}
