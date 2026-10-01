package retention

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// EnsurePartitions creates monthly telemetry partitions for the current month
// and the next `ahead` months, so new data stops landing in the default
// partition (which cannot be pruned by time). A month is skipped, and reported
// in the returned list, when the default partition already holds rows in that
// range: Postgres refuses to attach a partition over existing default rows, and
// moving them is a deliberate operation (see docs/scaling.md).
func EnsurePartitions(ctx context.Context, pool *pgxpool.Pool, now time.Time, ahead int) (created, skipped []string, err error) {
	base := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i <= ahead; i++ {
		from := base.AddDate(0, i, 0)
		to := from.AddDate(0, 1, 0)
		name := "telemetry_" + from.Format("2006_01")
		var exists bool
		if err = pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&exists); err != nil {
			return
		}
		if exists {
			continue
		}
		var inDefault bool
		if err = pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM telemetry_default WHERE observed_at >= $1 AND observed_at < $2)`, from, to).Scan(&inDefault); err != nil {
			return
		}
		if inDefault {
			skipped = append(skipped, name)
			continue
		}
		// Identifiers and bounds are built from a time value, never from input.
		q := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s PARTITION OF telemetry FOR VALUES FROM ('%s') TO ('%s')`,
			name, from.Format("2006-01-02"), to.Format("2006-01-02"))
		if _, err = pool.Exec(ctx, q); err != nil {
			return
		}
		created = append(created, name)
	}
	return
}
