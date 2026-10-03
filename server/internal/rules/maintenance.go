package rules

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
)

// InMaintenance reports whether a maintenance window is active for the device, either set on the
// device itself or on the asset it sits under (any ancestor). Errors count as "no": a failed lookup
// must never hide an alert.
func InMaintenance(ctx context.Context, pool *pgxpool.Pool, tenantID, deviceID string) bool {
	if deviceID == "" {
		return false
	}
	var on bool
	err := pool.QueryRow(ctx, `
		WITH RECURSIVE up AS (
		  SELECT a.id, a.parent_id, 1 AS depth FROM assets a JOIN devices d ON d.asset_id=a.id WHERE d.id=$2 AND d.tenant_id=$1
		  UNION ALL SELECT p.id, p.parent_id, up.depth+1 FROM assets p JOIN up ON p.id=up.parent_id WHERE p.tenant_id=$1 AND up.depth < 10)
		SELECT EXISTS (SELECT 1 FROM maintenance_windows w WHERE w.tenant_id=$1 AND w.ended_at IS NULL
		   AND now() >= w.starts_at AND now() < w.ends_at
		   AND (w.device_id=$2 OR w.asset_id IN (SELECT id FROM up)))`, tenantID, deviceID).Scan(&on)
	return err == nil && on
}

// Shelvable: critical alerts are never shelved. A maintenance window quiets warnings and info only.
func Shelvable(severity string) bool { return severity != "critical" }

// ReleaseShelved notifies, once, for alerts that were shelved during maintenance and are still open
// after no window covers their device any more, and clears the shelved flag so escalation applies.
func ReleaseShelved(ctx context.Context, pool *pgxpool.Pool, n Notifier) {
	rows, err := pool.Query(ctx, `SELECT id, tenant_id, COALESCE(device_id,''), severity, message FROM alerts WHERE shelved AND status='open' LIMIT 200`)
	if err != nil {
		log.Printf("maintenance: load shelved: %v", err)
		return
	}
	type item struct{ id, tenant, dev, sev, msg string }
	var list []item
	for rows.Next() {
		var it item
		if rows.Scan(&it.id, &it.tenant, &it.dev, &it.sev, &it.msg) == nil {
			list = append(list, it)
		}
	}
	rows.Close()
	for _, it := range list {
		if InMaintenance(ctx, pool, it.tenant, it.dev) {
			continue
		}
		tag, err := pool.Exec(ctx, `UPDATE alerts SET shelved=false WHERE id=$1 AND shelved`, it.id)
		if err != nil || tag.RowsAffected() == 0 {
			continue
		}
		dispatch(ctx, pool, n, it.tenant, it.sev, fmt.Sprintf("still active after maintenance: %s", it.msg))
	}
}
