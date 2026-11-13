package main

import (
	"context"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
)

// Real-PG cancelled/locked reads must not turn an unknown configuration into
// a successful empty cache entry that suppresses the next healthy reading.
func TestIntegrationPositionConfigReadFailure(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-pos-config-fault")
	ctx := t.Context()
	dev := "itest-pos-config-fault-dev"
	if _, err := s.st.Pool.Exec(ctx, `UPDATE devices SET lat_point='lat',lon_point='lon' WHERE id=$1`, dev); err != nil {
		t.Fatal(err)
	}
	tr := store.NewPositionTracker(s.st.Pool)
	at := time.Now().UTC().Truncate(time.Microsecond)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := tr.Track(cancelled, "itest-pos-config-fault", dev, "lat", 10, at); err == nil {
		t.Fatal("cancelled configuration read reported success")
	}
	// No Reset/TTL delay: the immediately following healthy pair must reload.
	for _, p := range []struct {
		key   string
		value float64
	}{{"lat", 10}, {"lon", 20}} {
		if err := tr.Track(ctx, "itest-pos-config-fault", dev, p.key, p.value, at); err != nil {
			t.Fatal(err)
		}
	}
	var lat, lon *float64
	if err := s.st.Pool.QueryRow(ctx, `SELECT lat,lon FROM devices WHERE id=$1`, dev).Scan(&lat, &lon); err != nil || lat == nil || lon == nil || *lat != 10 || *lon != 20 {
		t.Fatalf("healthy pair after cancelled config: %v %v %v", lat, lon, err)
	}
	tr.Reset()
	tx, err := s.st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `LOCK TABLE devices IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	deadline, done := context.WithTimeout(ctx, 100*time.Millisecond)
	err = tr.Track(deadline, "itest-pos-config-fault", dev, "lat", 11, at.Add(time.Second))
	done()
	if err == nil {
		t.Fatal("locked configuration read reported success")
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err = tr.Track(ctx, "itest-pos-config-fault", dev, "lat", 11, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = s.st.Pool.QueryRow(ctx, `SELECT lat FROM devices WHERE id=$1`, dev).Scan(&lat); err != nil || lat == nil || *lat != 11 {
		t.Fatalf("healthy reading after config lock timeout: %v %v", lat, err)
	}
	t.Log("cancelled/locked real configuration reads return errors and do not poison immediate healthy reload; no automatic replay of lost readings")
}

func TestIntegrationPositionConfigCacheTenantScope(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-pos-cache-scope")
	seed(t, s, "itest-pos-cache-other")
	ctx := t.Context()
	dev := "itest-pos-cache-scope-dev"
	if _, err := s.st.Pool.Exec(ctx, `UPDATE devices SET lat_point='lat',lon_point='lon' WHERE id=$1`, dev); err != nil {
		t.Fatal(err)
	}
	tr := store.NewPositionTracker(s.st.Pool)
	at := time.Now().UTC().Truncate(time.Microsecond)
	// A real not-found result may be cached, but only under its tenant+device.
	if err := tr.Track(ctx, "itest-pos-cache-other", dev, "lat", 99, at); err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		key   string
		value float64
	}{{"lat", 12}, {"lon", 22}} {
		if err := tr.Track(ctx, "itest-pos-cache-scope", dev, p.key, p.value, at); err != nil {
			t.Fatal(err)
		}
	}
	var lat, lon *float64
	if err := s.st.Pool.QueryRow(ctx, `SELECT lat,lon FROM devices WHERE id=$1`, dev).Scan(&lat, &lon); err != nil || lat == nil || lon == nil || *lat != 12 || *lon != 22 {
		t.Fatalf("foreign negative cache poisoned owner: %v %v %v", lat, lon, err)
	}
	// Conversely a populated owner cache cannot grant a foreign tenant config.
	if err := tr.Track(ctx, "itest-pos-cache-other", dev, "lat", 13, at.Add(time.Second)); err != nil {
		t.Fatalf("foreign miss should remain no-op, not reuse owner config: %v", err)
	}
	if err := tr.Track(ctx, "itest-pos-cache-other", "missing-device", "lat", 13, at); err != nil {
		t.Fatalf("genuine missing device should remain no-op: %v", err)
	}
}
