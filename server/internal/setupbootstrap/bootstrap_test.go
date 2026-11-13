package setupbootstrap

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

func sourceDir() string {
	if dir := os.Getenv("BOOTSTRAP_TEST_MIGRATIONS"); dir != "" {
		return dir
	}
	return filepath.Join("..", "..", "migrations")
}
func copyCatalog(t *testing.T, baseline bool) string {
	t.Helper()
	dir := t.TempDir()
	entries, e := os.ReadDir(sourceDir())
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") || (baseline && entry.Name() >= "0090_") {
			continue
		}
		b, e := os.ReadFile(filepath.Join(sourceDir(), entry.Name()))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, entry.Name()), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	return dir
}
func TestReviewedCatalogRefusesChangedMissingUnknownOrSymlinkFiles(t *testing.T) {
	for _, change := range []string{"none", "changed", "missing", "unknown", "symlink"} {
		t.Run(change, func(t *testing.T) {
			dir := copyCatalog(t, false)
			path := filepath.Join(dir, added[0])
			switch change {
			case "changed":
				os.WriteFile(path, []byte("SELECT 1"), 0600)
			case "missing":
				os.Remove(path)
			case "unknown":
				os.WriteFile(filepath.Join(dir, "0094_unknown.sql"), []byte("SELECT 1"), 0600)
			case "symlink":
				os.Remove(path)
				os.Symlink(filepath.Join(sourceDir(), added[0]), path)
			}
			_, e := catalog(dir)
			if (e == nil) != (change == "none") {
				t.Fatalf("%s: %v", change, e)
			}
		})
	}
}
func isolated(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, url)
	if e != nil {
		t.Fatal(e)
	}
	schema := "bootstrap_" + strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", "")
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	cfg, e := pgxpool.ParseConfig(url)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { pool.Close(); admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close() })
	return pool
}
func applyBaseline(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if e := migrate.Apply(ctx, p, copyCatalog(t, true)); e != nil {
		t.Fatal(e)
	}
	_, e := p.Exec(ctx, `INSERT INTO tenants(id,name) VALUES('retain-tenant','Preserve');INSERT INTO sites(id,tenant_id,name) VALUES('retain-site','retain-tenant','Preserve');INSERT INTO gateways(id,tenant_id,site_id,serial,status,cert_fingerprint) VALUES('retain-gw','retain-tenant','retain-site','KEEP-SERIAL','active','keep-fingerprint');INSERT INTO device_profiles(id,tenant_id,name,driver_profile,points,created_by) VALUES('retain-profile','retain-tenant','Keep source','modbus-generic','[]','synthetic');INSERT INTO devices(id,tenant_id,gateway_id,profile,name,config) VALUES('retain-device','retain-tenant','retain-gw','modbus-generic','Keep device','{"port":"unchanged","slave_id":11}')`)
	if e != nil {
		t.Fatal(e)
	}
}
func assertRows(t *testing.T, p *pgxpool.Pool, want int) {
	t.Helper()
	ctx := context.Background()
	var count int
	if e := p.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); e != nil || count != want {
		t.Fatalf("ledger %d want%d: %v", count, want, e)
	}
	var serial, fingerprint, config string
	if e := p.QueryRow(ctx, `SELECT g.serial,g.cert_fingerprint,d.config::text FROM gateways g JOIN devices d ON d.gateway_id=g.id WHERE g.id='retain-gw'`).Scan(&serial, &fingerprint, &config); e != nil || serial != "KEEP-SERIAL" || fingerprint != "keep-fingerprint" || !strings.Contains(config, "unchanged") || !strings.Contains(config, "11") {
		t.Fatalf("identity/source changed: %s %s %s %v", serial, fingerprint, config, e)
	}
}
func TestReviewedBootstrapAtomicAndRetainsExistingIdentitySource(t *testing.T) {
	p := isolated(t)
	applyBaseline(t, p)
	assertRows(t, p, 89)
	result, e := ApplyReviewed(context.Background(), p, sourceDir())
	if e != nil || len(result.Applied) != 4 || result.RuntimeAccepted {
		t.Fatalf("%+v %v", result, e)
	}
	assertRows(t, p, 93)
	var defaults string
	if e = p.QueryRow(context.Background(), `SELECT connection_defaults::text FROM device_profiles WHERE id='retain-profile'`).Scan(&defaults); e != nil || defaults != "{}" {
		t.Fatal(defaults, e)
	}
	result, e = ApplyReviewed(context.Background(), p, sourceDir())
	if e != nil || len(result.Applied) != 0 || result.RuntimeAccepted {
		t.Fatalf("repeat %+v %v", result, e)
	}
	assertRows(t, p, 93)
}
func TestRuntimeFailureRollsBackAllFourAdditionsAndLedgerTogether(t *testing.T) {
	p := isolated(t)
	applyBaseline(t, p)
	ctx := context.Background()
	_, e := p.Exec(ctx, `CREATE FUNCTION fail_last_bootstrap() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.filename='0093_edge_pairing_proofs.sql' THEN RAISE EXCEPTION 'synthetic injected interruption'; END IF; RETURN NEW; END$$;CREATE TRIGGER fail_bootstrap BEFORE INSERT ON schema_migrations FOR EACH ROW EXECUTE FUNCTION fail_last_bootstrap()`)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ApplyReviewed(ctx, p, sourceDir()); e == nil {
		t.Fatal("injected failure accepted")
	}
	assertRows(t, p, 89)
	var found bool
	if e = p.QueryRow(ctx, `SELECT to_regclass('edge_identity_migrations') IS NOT NULL OR to_regclass('edge_serial_port_inventory') IS NOT NULL OR to_regclass('edge_pairing_proofs') IS NOT NULL OR EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='device_profiles' AND column_name='connection_defaults')`).Scan(&found); e != nil || found {
		t.Fatalf("partial schema survived: %v %v", found, e)
	}
}
func TestUnknownPartialMissingAndUnrecordedSchemaFailBeforeMutation(t *testing.T) {
	for _, change := range []string{"missing", "unknown", "partial", "object", "column"} {
		t.Run(change, func(t *testing.T) {
			p := isolated(t)
			ctx := context.Background()
			if change != "missing" {
				applyBaseline(t, p)
			}
			switch change {
			case "unknown":
				p.Exec(ctx, "INSERT INTO schema_migrations(filename) VALUES('future.sql')")
			case "partial":
				p.Exec(ctx, "INSERT INTO schema_migrations(filename) VALUES($1)", added[0])
			case "object":
				p.Exec(ctx, "CREATE TABLE edge_identity_migrations(unrelated int)")
			case "column":
				p.Exec(ctx, "ALTER TABLE device_profiles ADD COLUMN connection_defaults text")
			}
			if _, e := ApplyReviewed(ctx, p, sourceDir()); e == nil {
				t.Fatal("unsafe baseline accepted")
			}
			var found bool
			if e := p.QueryRow(ctx, "SELECT to_regclass('edge_pairing_proofs') IS NOT NULL").Scan(&found); e != nil || found {
				t.Fatal(found, e)
			}
		})
	}
}

func TestContendedSharedMigrationLockIsBoundedBeforeMutation(t *testing.T) {
	p := isolated(t)
	applyBaseline(t, p)
	ctx := context.Background()
	holder, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer holder.Rollback(ctx)
	if _, e = holder.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", lockID); e != nil {
		t.Fatal(e)
	}
	started := time.Now()
	_, e = ApplyReviewed(ctx, p, sourceDir())
	elapsed := time.Since(started)
	if e == nil || !strings.Contains(e.Error(), "bounded wait") || elapsed < 4*time.Second || elapsed > 12*time.Second {
		t.Fatalf("lock bound: elapsed=%v error=%v", elapsed, e)
	}
	assertRows(t, p, 89)
	if _, e = holder.Exec(ctx, "SELECT 1"); e != nil {
		t.Fatal("holder should remain active", e)
	}
	if e = holder.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = ApplyReviewed(ctx, p, sourceDir()); e != nil {
		t.Fatal("fresh explicit retry after lock release", e)
	}
	assertRows(t, p, 93)
}
