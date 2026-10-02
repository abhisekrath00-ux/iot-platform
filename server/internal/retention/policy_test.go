package retention

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTenantPolicyAndDailyRollup(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for _, f := range []string{"0001_init.sql", "0014_telemetry_rollup.sql", "0022_retention_policy.sql"} {
		ddl, err := os.ReadFile("../../migrations/" + f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(ddl)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	// 0022 must be idempotent (migrations re-run on every API start).
	ddl, _ := os.ReadFile("../../migrations/0022_retention_policy.sql")
	if _, err := pool.Exec(ctx, string(ddl)); err != nil {
		t.Fatalf("0022 second run: %v", err)
	}
	ts := []string{"rtn-short", "rtn-default"}
	clean := func() {
		for _, x := range ts {
			pool.Exec(ctx, `DELETE FROM telemetry WHERE tenant_id=$1`, x)
			pool.Exec(ctx, `DELETE FROM telemetry_rollup_hourly WHERE tenant_id=$1`, x)
			pool.Exec(ctx, `DELETE FROM telemetry_rollup_daily WHERE tenant_id=$1`, x)
			pool.Exec(ctx, `DELETE FROM tenant_retention WHERE tenant_id=$1`, x)
		}
	}
	clean()
	t.Cleanup(clean)
	for _, x := range ts {
		pool.Exec(ctx, `INSERT INTO tenants(id,name) VALUES($1,$1) ON CONFLICT DO NOTHING`, x)
	}
	now := time.Now().UTC().Truncate(time.Hour)
	old := now.Add(-40 * 24 * time.Hour).Truncate(24 * time.Hour).Add(3 * time.Hour)
	recent := now.Add(-2 * time.Hour)
	ins := func(tenant, id string, at time.Time, v float64) {
		if _, err := pool.Exec(ctx, `INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,value,unit,quality,schema_version)
		  VALUES($1,$2,'g','d','p',$3,$4,'C','measured',1)`, id, tenant, at, v); err != nil {
			t.Fatal(err)
		}
	}
	for _, x := range ts {
		ins(x, x+"-old1", old, 10)
		ins(x, x+"-old2", old.Add(time.Hour), 20)
		ins(x, x+"-new", recent, 5)
	}
	// rtn-short keeps 7 days raw and 35 days hourly; rtn-default has no policy.
	if _, err := pool.Exec(ctx, `INSERT INTO tenant_retention(tenant_id, raw_days, hourly_days) VALUES('rtn-short',7,35)`); err != nil {
		t.Fatal(err)
	}
	ApplyTenantPolicies(ctx, pool, now)
	count := func(q, tenant string) int {
		var n int
		if err := pool.QueryRow(ctx, q, tenant).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT count(*) FROM telemetry WHERE tenant_id=$1`, "rtn-short"); n != 1 {
		t.Fatalf("rtn-short raw rows = %d, want 1 (old purged, recent kept)", n)
	}
	if n := count(`SELECT count(*) FROM telemetry WHERE tenant_id=$1`, "rtn-default"); n != 3 {
		t.Fatalf("tenant without a policy lost rows: %d", n)
	}
	// 40-day-old hourly rows are older than 35 days: purged, but daily kept with the right sums.
	if n := count(`SELECT count(*) FROM telemetry_rollup_hourly WHERE tenant_id=$1 AND bucket < now() - interval '35 days'`, "rtn-short"); n != 0 {
		t.Fatalf("old hourly rows remain: %d", n)
	}
	var n int64
	var sum, mn, mx float64
	if err := pool.QueryRow(ctx, `SELECT n, sum, min, max FROM telemetry_rollup_daily WHERE tenant_id='rtn-short' AND bucket < now() - interval '35 days'`).Scan(&n, &sum, &mn, &mx); err != nil {
		t.Fatalf("daily rollup missing: %v", err)
	}
	if n != 2 || sum != 30 || mn != 10 || mx != 20 {
		t.Fatalf("daily = n%d sum%v min%v max%v", n, sum, mn, mx)
	}
	// Re-running is a no-op.
	ApplyTenantPolicies(ctx, pool, now)
	if c := count(`SELECT count(*) FROM telemetry_rollup_daily WHERE tenant_id=$1`, "rtn-short"); c != 1 {
		t.Fatalf("daily rows after rerun = %d", c)
	}
	// Global purge must skip the tenant with its own policy and still purge the other.
	if _, err := Purge(ctx, pool, now.Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if c := count(`SELECT count(*) FROM telemetry WHERE tenant_id=$1`, "rtn-default"); c != 1 {
		t.Fatalf("global purge on default tenant left %d rows, want 1", c)
	}
}
