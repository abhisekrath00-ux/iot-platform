package retention

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PurgePositions removes expired trail samples, including inactive devices.
// One pass deletes at most 50,000 rows in 5,000-row transactions. A backlog
// continues on the next hourly pass; the API window excludes old rows while
// cleanup catches up. Use one fixed cutoff for the entire pass.
func PurgePositions(ctx context.Context, pool *pgxpool.Pool, now time.Time) (int64, error) {
	cutoff := now.Add(-7 * 24 * time.Hour)
	var total int64
	for batch := 0; batch < 10; batch++ {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		ct, err := pool.Exec(ctx, `DELETE FROM device_positions p USING (
   SELECT device_id,recorded_at FROM device_positions WHERE recorded_at<$1
   ORDER BY recorded_at,device_id LIMIT 5000 FOR UPDATE SKIP LOCKED
  ) expired WHERE p.device_id=expired.device_id AND p.recorded_at=expired.recorded_at`, cutoff)
		if err != nil {
			return total, err
		}
		total += ct.RowsAffected()
		if ct.RowsAffected() < 5000 {
			break
		}
	}
	return total, nil
}
