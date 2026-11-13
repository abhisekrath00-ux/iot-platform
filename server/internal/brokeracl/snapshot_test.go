package brokeracl

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSnapshotCertificateACLIsCompleteReadOnlyAndRefusesUnsupported(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, url)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close()
	schema := "acl_snapshot_" + strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", "")
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, e := pgxpool.ParseConfig(url)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	_, e = pool.Exec(ctx, `CREATE TABLE gateways(serial text,tenant_id text,id text,kind text,status text,auth_mode text,cert_fingerprint text);INSERT INTO gateways VALUES('A','tenant-a','gw-a','edge','active','certificate','fp-a'),('B','tenant-b','gw-b','direct','active','certificate','fp-b'),('old','tenant-a','gw-old','edge','revoked','certificate','fp-old'),('lab','tenant-a','gw-lab','edge','active','certificate',NULL)`)
	if e != nil {
		t.Fatal(e)
	}
	acl, n, e := SnapshotCertificateACL(ctx, pool)
	if e != nil || n != 2 {
		t.Fatal(n, e)
	}
	if !strings.Contains(acl, "user A\ntopic write t/tenant-a/g/gw-a/telemetry") || !strings.Contains(acl, "user B\ntopic write t/tenant-b/g/gw-b/telemetry") || strings.Contains(acl, "user old") || strings.Contains(acl, "user lab") {
		t.Fatal("incomplete or stale ACL", acl)
	}
	b := acl[strings.Index(acl, "user B"):]
	if strings.Contains(b, "topic read") || strings.Contains(b, "/cmd") {
		t.Fatal("direct device gained control grants")
	}
	for _, bad := range []string{"api", "ingest", "A", "evil\nuser api", "wild+#", "with space"} {
		_, e = pool.Exec(ctx, `INSERT INTO gateways VALUES($1,'tenant-c','gw-c','edge','active','certificate','fp-c')`, bad)
		if e != nil {
			t.Fatal(e)
		}
		if a, _, err := SnapshotCertificateACL(ctx, pool); err == nil || a != "" {
			t.Fatal("unsafe snapshot accepted", bad)
		}
		pool.Exec(ctx, `DELETE FROM gateways WHERE tenant_id='tenant-c'`)
	}
	pool.Exec(ctx, `INSERT INTO gateways VALUES('pw','tenant-c','pw','direct','active','password',NULL)`)
	if a, _, err := SnapshotCertificateACL(ctx, pool); err == nil || a != "" {
		t.Fatal("password device silently dropped")
	}
	var count int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM gateways`).Scan(&count); e != nil || count != 5 {
		t.Fatal("export mutated database", count, e)
	}
	pool.Close()
	if a, _, err := SnapshotCertificateACL(ctx, pool); err == nil || a != "" {
		t.Fatal("DB failure produced ACL")
	}
}
