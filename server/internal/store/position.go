package store

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PositionTTL is how long a device's position-point config is cached in the
// process. A config change via the API takes up to this long to reach ingest.
const PositionTTL = time.Minute

// PositionTracker updates devices.lat/lon when readings arrive on the points
// the device names as its latitude and longitude sources, so the map and
// geofence alerts follow reported positions, not only hand-entered ones.
// Position is metadata: a bad value is dropped, never an ingest error.
type PositionTracker struct {
	pool *pgxpool.Pool
	mu   sync.Mutex
	cfg  map[string]posCfg
}

type posCfg struct {
	latPt, lonPt string
	lat, lon     *float64
	expires      time.Time
}

func NewPositionTracker(pool *pgxpool.Pool) *PositionTracker {
	return &PositionTracker{pool: pool, cfg: map[string]posCfg{}}
}

// Reset drops every cached config; the next reading per device reloads from
// the database. Tests use it to skip the TTL.
func (t *PositionTracker) Reset() {
	t.mu.Lock()
	t.cfg = map[string]posCfg{}
	t.mu.Unlock()
}

// Track reports one accepted reading. It is a no-op unless the device's
// position points are configured and point is one of them.
func (t *PositionTracker) Track(ctx context.Context, tenant, device, point string, value float64) error {
	c, err := t.config(ctx, tenant, device)
	if err != nil {
		return err
	}
	isLat, isLon := point == c.latPt, point == c.lonPt
	if !isLat && !isLon {
		return nil
	}
	// The axis this reading carries must be in range; the other axis keeps
	// whatever is stored. Lat and lon often arrive as separate messages, so a
	// half fix is stored and the map simply waits for both axes.
	if isLat && !validAxis(value, 90) || isLon && !validAxis(value, 180) {
		return nil
	}
	lat, lon := c.lat, c.lon
	if isLat {
		lat = &value
	} else {
		lon = &value
	}
	if _, err := t.pool.Exec(ctx, `UPDATE devices SET lat=$1, lon=$2 WHERE id=$3 AND tenant_id=$4`, lat, lon, device, tenant); err != nil {
		return err
	}
	t.mu.Lock()
	c.lat, c.lon = lat, lon
	t.cfg[device] = c
	t.mu.Unlock()
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
	var lat, lon *float64
	err := t.pool.QueryRow(ctx,
		`SELECT lat_point, lon_point, lat, lon FROM devices WHERE id=$1 AND tenant_id=$2`, device, tenant).
		Scan(&latPt, &lonPt, &lat, &lon)
	if err != nil {
		// Unknown device: cache an empty config so hot telemetry paths do
		// not re-query every reading.
		latPt, lonPt, lat, lon = nil, nil, nil, nil
	}
	c = posCfg{lat: lat, lon: lon, expires: time.Now().Add(PositionTTL)}
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
