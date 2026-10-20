package main

import (
	"context"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/rules"
)

// Geofence enter/exit alerts: the first sighting of a device/fence pair is a
// silent baseline; only real transitions after that raise alerts, and only
// when the fence asks for that direction.
func TestIntegrationGeofenceAlerts(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-gf1")
	ctx := context.Background()
	pool := s.st.Pool
	clean := func() {
		for _, q := range []string{
			`DELETE FROM alerts WHERE tenant_id='itest-gf1'`,
			`DELETE FROM geofence_state WHERE tenant_id='itest-gf1'`,
			`DELETE FROM geofences WHERE tenant_id='itest-gf1'`,
			`DELETE FROM devices WHERE tenant_id='itest-gf1' AND id LIKE 'itest-gf1-%'`,
		} {
			pool.Exec(ctx, q)
		}
	}
	clean()
	t.Cleanup(clean)
	for _, q := range []string{
		// yard: alerts both ways at critical; quiet: alerts off entirely
		`INSERT INTO geofences(id,tenant_id,name,lat,lon,radius_m,alert_on_enter,alert_on_exit,alert_severity) VALUES
		 ('gf-yard','itest-gf1','Yard',10.0,20.0,100,true,true,'critical'),
		 ('gf-quiet','itest-gf1','Quiet',10.0,20.0,100,false,false,'warning')`,
		// d1 starts outside (about 1.1 km north), d2 starts inside
		`INSERT INTO devices(id,tenant_id,gateway_id,profile,name,lat,lon) VALUES
		 ('itest-gf1-d1','itest-gf1','itest-gf1-gw','modbus-tcp','Truck one',10.01,20.0),
		 ('itest-gf1-d2','itest-gf1','itest-gf1-gw','modbus-tcp','Truck two',10.0,20.0)`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	setPos := func(dev string, lat, lon float64) {
		if _, err := pool.Exec(ctx, `UPDATE devices SET lat=$1, lon=$2 WHERE id=$3`, lat, lon, dev); err != nil {
			t.Fatal(err)
		}
	}
	count := func(dev, fence string) (n int) {
		pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE device_id=$1 AND geofence_id=$2`, dev, fence).Scan(&n)
		return
	}
	eval := func() { rules.EvaluateGeofences(ctx, pool, nil) }

	// Baseline pass: d2 sits inside both fences, d1 outside; nothing alerts.
	eval()
	if n := count("itest-gf1-d1", "gf-yard") + count("itest-gf1-d2", "gf-yard") + count("itest-gf1-d2", "gf-quiet"); n != 0 {
		t.Fatalf("baseline raised %d alerts, want 0", n)
	}

	// d1 drives into the yard: one "entered" alert at the fence's severity.
	setPos("itest-gf1-d1", 10.0, 20.0)
	eval()
	if n := count("itest-gf1-d1", "gf-yard"); n != 1 {
		t.Fatalf("after enter: %d alerts, want 1", n)
	}
	var sev, msg string
	if err := pool.QueryRow(ctx, `SELECT severity, message FROM alerts WHERE device_id='itest-gf1-d1' AND geofence_id='gf-yard'`).Scan(&sev, &msg); err != nil {
		t.Fatal(err)
	}
	if sev != "critical" {
		t.Fatalf("severity %q, want critical", sev)
	}
	if want := `Truck one entered zone "Yard"`; msg != want {
		t.Fatalf("message %q, want %q", msg, want)
	}
	// The quiet fence saw the same move but stays silent.
	if n := count("itest-gf1-d1", "gf-quiet"); n != 0 {
		t.Fatalf("quiet fence raised %d alerts, want 0", n)
	}

	// No repeat while the truck stays put.
	eval()
	if n := count("itest-gf1-d1", "gf-yard"); n != 1 {
		t.Fatalf("steady state raised %d alerts, want 1", n)
	}

	// Leaving raises the exit alert; the open enter alert dedupes nothing new.
	setPos("itest-gf1-d1", 10.01, 20.0)
	eval()
	if n := count("itest-gf1-d1", "gf-yard"); n != 2 {
		t.Fatalf("after exit: %d alerts, want 2", n)
	}
	var exitMsg string
	if err := pool.QueryRow(ctx, `SELECT message FROM alerts WHERE device_id='itest-gf1-d1' AND geofence_id='gf-yard' ORDER BY created_at DESC LIMIT 1`).Scan(&exitMsg); err != nil {
		t.Fatal(err)
	}
	if want := `Truck one left zone "Yard"`; exitMsg != want {
		t.Fatalf("exit message %q, want %q", exitMsg, want)
	}

	// Re-entering while the enter alert is still open does not stack a second open one.
	setPos("itest-gf1-d1", 10.0, 20.0)
	eval()
	var open int
	pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE device_id='itest-gf1-d1' AND geofence_id='gf-yard' AND status='open'`).Scan(&open)
	if open != 2 { // original enter + exit, both still open; no third
		t.Fatalf("open alerts %d, want 2 (flap deduped)", open)
	}

	// A device with no position keeps its last state and alerts nowhere.
	if _, err := pool.Exec(ctx, `UPDATE devices SET lat=NULL, lon=NULL WHERE id='itest-gf1-d2'`); err != nil {
		t.Fatal(err)
	}
	eval()
	if n := count("itest-gf1-d2", "gf-yard"); n != 0 {
		t.Fatalf("cleared position raised %d alerts, want 0", n)
	}
}
