package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSetupCapabilityProtocolContainsNoDeploymentApproval(t *testing.T) {
	b, e := json.Marshal(currentSetupCapabilities())
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(b), `"activates_deployment":false`) || !strings.Contains(string(b), "certificate_acl_snapshot_v1") {
		t.Fatal(string(b))
	}
	for _, x := range []struct {
		packaged, applied []string
		want              string
	}{
		{[]string{"a.sql"}, []string{"a.sql"}, "migration_ledger_current"},
		{[]string{"a.sql", "b.sql"}, []string{"a.sql"}, "application_bootstrap_required"},
		{[]string{"a.sql"}, []string{"a.sql", "future.sql"}, "application_bootstrap_required"},
		{nil, nil, "application_bootstrap_required"},
	} {
		value := compareSetupMigrations(x.packaged, x.applied)
		if value.Status != x.want || value.DeploymentApproved {
			t.Fatalf("%+v", value)
		}
	}
}
func TestSetupReadinessIsReadOnlyAndNeverCreatesMissingLedger(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, url)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close()
	schema := "setup_readiness_" + strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", "")
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, e := pgxpool.ParseConfig(url)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	p, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	dir := t.TempDir()
	if e = os.WriteFile(filepath.Join(dir, "a.sql"), []byte("synthetic file, never executed"), 0600); e != nil {
		t.Fatal(e)
	}
	value, e := inspectSetupSchema(ctx, p, dir)
	if e != nil || value.Status != "application_bootstrap_required" || len(value.Pending) != 1 {
		t.Fatalf("%+v %v", value, e)
	}
	var exists bool
	if e = p.QueryRow(ctx, "SELECT to_regclass('schema_migrations') IS NOT NULL").Scan(&exists); e != nil || exists {
		t.Fatalf("inspection created ledger: %v %v", exists, e)
	}
	if _, e = p.Exec(ctx, "CREATE TABLE schema_migrations(filename text PRIMARY KEY);INSERT INTO schema_migrations VALUES ('a.sql')"); e != nil {
		t.Fatal(e)
	}
	value, e = inspectSetupSchema(ctx, p, dir)
	if e != nil || value.Status != "migration_ledger_current" || value.AppliedCount != 1 || value.DeploymentApproved {
		t.Fatalf("%+v %v", value, e)
	}
	if _, e = p.Exec(ctx, "INSERT INTO schema_migrations VALUES ('future.sql')"); e != nil {
		t.Fatal(e)
	}
	value, e = inspectSetupSchema(ctx, p, dir)
	if e != nil || value.Status != "application_bootstrap_required" || len(value.Unknown) != 1 {
		t.Fatalf("%+v %v", value, e)
	}
	if _, e = inspectSetupSchema(ctx, p, filepath.Join(dir, "missing")); e == nil {
		t.Fatal("missing package accepted")
	}
	var count int
	if e = p.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); e != nil || count != 2 {
		t.Fatalf("ledger changed: %d %v", count, e)
	}
}
