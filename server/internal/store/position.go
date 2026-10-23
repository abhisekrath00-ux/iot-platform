package store

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PositionTTL is how long a device's position-point config is cached in the
// process. A config change via the API takes up to this long to reach ingest.
const PositionTTL = time.Minute

// PositionPairWindow is the largest time gap between a device's latitude and
// longitude fixes that still counts as one position. A wider gap means the
// device may have moved between the two readings, so pairing them could place
// it somewhere it never was (and trip a geofence falsely). GPS trackers that
// send both axes in one message always agree; stragglers get two minutes.
const PositionPairWindow = 2 * time.Minute

// PositionTracker turns readings on a device's configured position points
// into devices.lat/lon updates. Freshness and provenance rules:
//   - each axis keeps its own value and observation time (devices.lat_fix /
//     lat_fix_at, lon_fix / lon_fix_at), shared across processes via the row;
//   - a reading older than (or as old as) the stored fix for its axis is
//     dropped, so replays, duplicates and out-of-order batches never move a
//     device backwards;
//   - devices.lat/lon is published only when both axes exist and their fix
//     times agree within PositionPairWindow;
//   - a hand-set position (PUT /location) stands until the next paired fix,
//     and clearing the source freezes the last published position.
//
// Position is metadata: a bad value is dropped, never an ingest error.
type PositionTracker struct {
	pool *pgxpool.Pool
	mu   sync.Mutex
	cfg  map[string]posCfg
}

type posCfg struct {
	latPt, lonPt string
	expires      time.Time
}

func NewPositionTracker(pool *pgxpool.Pool) *PositionTracker {
	return &PositionTracker{pool: pool, cfg: map[string]posCfg{}}
}

// Reset drops every cached config; the next reading per device reloads from
// the database. The API calls it after a config change so its own ingest path
// picks the change up at once; tests use it to skip the TTL.
func (t *PositionTracker) Reset() {
	t.mu.Lock()
	t.cfg = map[string]posCfg{}
	t.mu.Unlock()
}

// Track reports one accepted and newly stored reading. observedAt is the
// reading's own timestamp, not the arrival time. It is a no-op unless the
// device's position points are configured and point is one of them.
func (t *PositionTracker) Track(ctx context.Context, tenant, device, point string, value float64, observedAt time.Time) error {
	c, err := t.config(ctx, tenant, device)
	if err != nil {
		return err
	}
	isLat, isLon := point == c.latPt, point == c.lonPt
	if !isLat && !isLon {
		return nil
	}
	if isLat && !validAxis(value, 90) || isLon && !validAxis(value, 180) {
		return nil
	}
	// Serialize on the device row so the two ingest processes cannot split a
	// read-modify-write of the axis pair.
	tx, err := t.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var latV, lonV *float64
	var latT, lonT *time.Time
	if err := tx.QueryRow(ctx,
		`SELECT lat_fix, lat_fix_at, lon_fix, lon_fix_at FROM devices WHERE id=$1 AND tenant_id=$2 FOR UPDATE`,
		device, tenant).Scan(&latV, &latT, &lonV, &lonT); err != nil {
		return err
	}
	if isLat {
		if latT != nil && !observedAt.After(*latT) {
			return nil // stale, duplicate or out-of-order
		}
		latV, latT = &value, &observedAt
	} else {
		if lonT != nil && !observedAt.After(*lonT) {
			return nil
		}
		lonV, lonT = &value, &observedAt
	}
	var pubLat, pubLon *float64
	if latV != nil && lonV != nil && latT != nil && lonT != nil {
		d := latT.Sub(*lonT)
		if d < 0 {
			d = -d
		}
		if d <= PositionPairWindow {
			pubLat, pubLon = latV, lonV
		}
	}
	_, err = tx.Exec(ctx,
		`UPDATE devices SET lat_fix=$1, lat_fix_at=$2, lon_fix=$3, lon_fix_at=$4,
		   lat = COALESCE($5, lat), lon = COALESCE($6, lon)
		 WHERE id=$7 AND tenant_id=$8`,
		latV, latT, lonV, lonT, pubLat, pubLon, device, tenant)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if pubLat != nil {
		fixAt := *latT
		if lonT.After(fixAt) {
			fixAt = *lonT
		}
		if err := RecordTrail(ctx, t.pool, tenant, device, *pubLat, *pubLon, fixAt, "telemetry"); err != nil {
			log.Printf("position trail: %v", err)
		}
	}
	return nil
}

func (t *PositionTracker) config(ctx context.Context, tenant, device string) (posCfg, error) {
	t.mu.Lock()
	c, ok := t.cfg[device]
	t.mu.Unlock()
	if ok && time.Now().Before(c.expires) {
		return c, nil
	}
	var latPt, lonPt *string
	err := t.pool.QueryRow(ctx,
		`SELECT lat_point, lon_point FROM devices WHERE id=$1 AND tenant_id=$2`, device, tenant).
		Scan(&latPt, &lonPt)
	if err != nil {
		// Unknown device: cache an empty config so hot telemetry paths do
		// not re-query every reading.
		latPt, lonPt = nil, nil
	}
	c = posCfg{expires: time.Now().Add(PositionTTL)}
	if latPt != nil {
		c.latPt = *latPt
	}
	if lonPt != nil {
		c.lonPt = *lonPt
	}
	t.mu.Lock()
	t.cfg[device] = c
	t.mu.Unlock()
	return c, nil
}

func validAxis(v, limit float64) bool {
	return v >= -limit && v <= limit && v == v // NaN fails v == v
}
