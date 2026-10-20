package rules

import (
	"context"
	"fmt"
	"log"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Geofence enter/exit alerts. Devices carry a hand-entered position (see
// cmd/api/geomap.go); zones are named circles. EvaluateGeofences diffs each
// positioned device against every alert-enabled fence, records membership in
// geofence_state, and raises one alert per real transition. The first time a
// device/fence pair is seen the state is recorded silently, so turning alerts
// on for a zone that already contains devices does not raise a storm.
// A device with no position keeps its last known state - clearing a position
// is not treated as leaving every zone.

type fenceRow struct {
	id, tenant, name string
	lat, lon, radius float64
	onEnter, onExit  bool
	severity         string
}

func haversineM(lat1, lon1, lat2, lon2 float64) float64 {
	const earth = 6371000.0
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dp, dl := (lat2-lat1)*math.Pi/180, (lon2-lon1)*math.Pi/180
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * earth * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

// EvaluateGeofences runs one diff pass over all tenants. Called once a minute
// by the single-replica leader loop in main.go.
func EvaluateGeofences(ctx context.Context, pool *pgxpool.Pool, n Notifier) {
	fences, err := pool.Query(ctx,
		`SELECT id, tenant_id, name, lat, lon, radius_m, alert_on_enter, alert_on_exit, alert_severity
		 FROM geofences WHERE alert_on_enter OR alert_on_exit`)
	if err != nil {
		log.Printf("geofence eval: list fences: %v", err)
		return
	}
	var fs []fenceRow
	for fences.Next() {
		var f fenceRow
		if err := fences.Scan(&f.id, &f.tenant, &f.name, &f.lat, &f.lon, &f.radius, &f.onEnter, &f.onExit, &f.severity); err == nil {
			fs = append(fs, f)
		}
	}
	fences.Close()
	if len(fs) == 0 {
		return
	}
	for _, f := range fs {
		evalFence(ctx, pool, n, f)
	}
}

func evalFence(ctx context.Context, pool *pgxpool.Pool, n Notifier, f fenceRow) {
	devs, err := pool.Query(ctx,
		`SELECT id, COALESCE(NULLIF(name,''),id), lat, lon FROM devices
		 WHERE tenant_id=$1 AND lat IS NOT NULL AND lon IS NOT NULL LIMIT 5000`, f.tenant)
	if err != nil {
		log.Printf("geofence eval: list devices: %v", err)
		return
	}
	type dev struct {
		id, name string
		lat, lon float64
	}
	var ds []dev
	for devs.Next() {
		var d dev
		if err := devs.Scan(&d.id, &d.name, &d.lat, &d.lon); err == nil {
			ds = append(ds, d)
		}
	}
	devs.Close()
	for _, d := range ds {
		inside := haversineM(d.lat, d.lon, f.lat, f.lon) <= f.radius
		var wasInside bool
		err := pool.QueryRow(ctx,
			`SELECT inside FROM geofence_state WHERE device_id=$1 AND geofence_id=$2`, d.id, f.id).Scan(&wasInside)
		if err != nil {
			// First sighting of this pair: record the baseline silently.
			if _, err := pool.Exec(ctx,
				`INSERT INTO geofence_state(tenant_id,device_id,geofence_id,inside) VALUES($1,$2,$3,$4)
				 ON CONFLICT (device_id,geofence_id) DO UPDATE SET inside=$4, updated_at=now()`,
				f.tenant, d.id, f.id, inside); err != nil {
				log.Printf("geofence eval: baseline: %v", err)
			}
			continue
		}
		if wasInside == inside {
			continue
		}
		if _, err := pool.Exec(ctx,
			`UPDATE geofence_state SET inside=$1, updated_at=now() WHERE device_id=$2 AND geofence_id=$3`,
			inside, d.id, f.id); err != nil {
			log.Printf("geofence eval: update state: %v", err)
			continue
		}
		entered := inside
		if (entered && !f.onEnter) || (!entered && !f.onExit) {
			continue
		}
		verb := "left"
		if entered {
			verb = "entered"
		}
		msg := fmt.Sprintf("%s %s zone %q", d.name, verb, f.name)
		severity := f.severity
		if severity != "info" && severity != "warning" && severity != "critical" {
			severity = "warning"
		}
		// A flapping position updates state but does not stack duplicate open
		// alerts: dedupe on the same transition (message carries the verb and
		// zone), so an open "entered" still lets the later "left" through.
		var existing string
		dedupe := pool.QueryRow(ctx,
			`SELECT id FROM alerts WHERE geofence_id=$1 AND device_id=$2 AND status='open' AND message=$3 LIMIT 1`, f.id, d.id, msg).Scan(&existing)
		if dedupe == nil {
			continue
		}
		shelved := Shelvable(severity) && InMaintenance(ctx, pool, f.tenant, d.id)
		if _, err := pool.Exec(ctx,
			`INSERT INTO alerts(id,tenant_id,rule_id,geofence_id,severity,message,device_id,shelved) VALUES($1,$2,NULL,$3,$4,$5,$6,$7)`,
			uuid.NewString(), f.tenant, f.id, severity, msg, d.id, shelved); err != nil {
			log.Printf("geofence eval: insert alert: %v", err)
			continue
		}
		if !shelved {
			dispatch(ctx, pool, n, f.tenant, severity, msg)
		}
	}
}
