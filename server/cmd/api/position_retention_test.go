package main

import (
	"context"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/retention"
)

func TestIntegrationPositionRetention(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-posret")
	p := s.st.Pool
	ctx := t.Context()
	clean := func() { p.Exec(context.Background(), `DELETE FROM device_positions WHERE tenant_id='itest-posret'`) }
	clean()
	t.Cleanup(clean)
	now := time.Now().UTC().Truncate(time.Microsecond)
	cutoff := now.Add(-7 * 24 * time.Hour)
	// No device movement is needed for global expiry. Exact cutoff and recent
	// samples remain; older rows disappear independently of telemetry policy.
	for _, at := range []time.Time{cutoff.Add(-time.Second), cutoff, now} {
		if _, err := p.Exec(ctx, `INSERT INTO device_positions VALUES('itest-posret','itest-posret-dev',10,20,$1,'manual')`, at); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := retention.PurgePositions(ctx, p, now); err != nil {
		t.Fatal(err)
	}
	var n int
	p.QueryRow(ctx, `SELECT count(*) FROM device_positions WHERE tenant_id='itest-posret'`).Scan(&n)
	if n != 2 {
		t.Fatalf("boundary count %d", n)
	}
	if _, err := retention.PurgePositions(ctx, p, now); err != nil {
		t.Fatal(err)
	}
	p.QueryRow(ctx, `SELECT count(*) FROM device_positions WHERE tenant_id='itest-posret'`).Scan(&n)
	if n != 2 {
		t.Fatal("not idempotent")
	}
	// Exercise the bounded backlog, not just the happy path. No other tenant
	// receives recent-row deletion, and one pass is capped at fifty thousand.
	if _, err := p.Exec(ctx, `INSERT INTO device_positions SELECT 'itest-posret','itest-posret-dev',10,20,$1::timestamptz-g*interval '1 second','telemetry' FROM generate_series(2,50002)g`, cutoff); err != nil {
		t.Fatal(err)
	}
	deleted, err := retention.PurgePositions(ctx, p, now)
	if err != nil || deleted != 50000 {
		t.Fatalf("cap %d %v", deleted, err)
	}
	p.QueryRow(ctx, `SELECT count(*) FROM device_positions WHERE tenant_id='itest-posret' AND recorded_at<$1`, cutoff).Scan(&n)
	if n != 1 {
		t.Fatalf("backlog %d", n)
	}
	deleted, err = retention.PurgePositions(ctx, p, now)
	if err != nil || deleted != 1 {
		t.Fatalf("second pass %d %v", deleted, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := retention.PurgePositions(cancelled, p, now); err == nil {
		t.Fatal("cancel ignored")
	}
}
