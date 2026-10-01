package retention

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestEnsurePartitions(t *testing.T) {
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
	// Schema v1 is idempotent; apply it so the test also works on a fresh database.
	ddl, err := os.ReadFile("../../migrations/0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(ddl)); err != nil {
		t.Fatal(err)
	}
	// Far-future months so the test never collides with real data.
	now := time.Date(2090, 5, 15, 0, 0, 0, 0, time.UTC)
	clean := func() {
		for _, n := range []string{"2090_05", "2090_06", "2090_07"} {
			pool.Exec(ctx, "DROP TABLE IF EXISTS telemetry_"+n)
		}
		pool.Exec(ctx, `DELETE FROM telemetry_default WHERE observed_at >= '2090-01-01'`)
	}
	clean()
	t.Cleanup(clean)
	c, sk, err := EnsurePartitions(ctx, pool, now, 2)
	if err != nil || len(c) != 3 || len(sk) != 0 {
		t.Fatalf("first run: %v %v %v", c, sk, err)
	}
	if c, _, err := EnsurePartitions(ctx, pool, now, 2); err != nil || len(c) != 0 {
		t.Fatalf("second run must be a no-op: %v %v", c, err)
	}
	// Rows in default block creation for that month and are reported.
	pool.Exec(ctx, `DROP TABLE telemetry_2090_07`)
	if _, err := pool.Exec(ctx, `INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,value,unit,quality,schema_version)
	  VALUES('part-x','t','g','d','p','2090-07-10T00:00:00Z',1,'C','measured',1)`); err != nil {
		t.Fatal(err)
	}
	c, sk, err = EnsurePartitions(ctx, pool, now, 2)
	if err != nil || len(c) != 0 || len(sk) != 1 || sk[0] != "telemetry_2090_07" {
		t.Fatalf("default-has-rows: %v %v %v", c, sk, err)
	}
}
