package ingestprocessing

import (
	"context"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
	"github.com/jackc/pgx/v5"
)

// applyPosition consumes the admission's position source configuration, not a
// cached or newly substituted mapping. The caller verifies current policy and
// keeps this stage, trail, flow history and attempt progress in ONE transaction.
// Device-row serialization preserves per-axis monotonic observation time across
// concurrent instances; stale/duplicate/invalid fixes are recorded no-ops, not
// errors that hold otherwise valid telemetry. Other unsupported stages still
// quarantine the whole plan before reaching here.
func applyPosition(ctx context.Context, tx pgx.Tx, p Plan) error {
	e := p.Envelope
	isLat, isLon := e.PointID == p.Device.LatPoint, e.PointID == p.Device.LonPoint
	if !isLat && !isLon {
		return nil
	}
	if (isLat && (e.Value < -90 || e.Value > 90)) || (isLon && (e.Value < -180 || e.Value > 180)) {
		return nil
	}
	var lat, lon *float64
	var latAt, lonAt *time.Time
	err := tx.QueryRow(ctx, `SELECT lat_fix,lat_fix_at,lon_fix,lon_fix_at FROM devices WHERE id=$1 AND tenant_id=$2 AND gateway_id=$3 FOR UPDATE`, e.DeviceID, e.TenantID, e.GatewayID).Scan(&lat, &latAt, &lon, &lonAt)
	if err != nil {
		return err
	}
	if isLat {
		if latAt != nil && !e.ObservedAt.After(*latAt) {
			return nil
		}
		lat, latAt = &e.Value, &e.ObservedAt
	} else {
		if lonAt != nil && !e.ObservedAt.After(*lonAt) {
			return nil
		}
		lon, lonAt = &e.Value, &e.ObservedAt
	}
	var pubLat, pubLon *float64
	if lat != nil && lon != nil && latAt != nil && lonAt != nil {
		d := latAt.Sub(*lonAt)
		if d < 0 {
			d = -d
		}
		if d <= store.PositionPairWindow {
			pubLat, pubLon = lat, lon
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE devices SET lat_fix=$1,lat_fix_at=$2,lon_fix=$3,lon_fix_at=$4,lat=COALESCE($5,lat),lon=COALESCE($6,lon) WHERE id=$7 AND tenant_id=$8 AND gateway_id=$9`, lat, latAt, lon, lonAt, pubLat, pubLon, e.DeviceID, e.TenantID, e.GatewayID); err != nil {
		return err
	}
	if pubLat == nil {
		return nil
	}
	fixAt := *latAt
	if lonAt.After(fixAt) {
		fixAt = *lonAt
	}
	return store.RecordTrailTx(ctx, tx, e.TenantID, e.DeviceID, *pubLat, *pubLon, fixAt, "telemetry")
}
