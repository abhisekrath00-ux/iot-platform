package processingschema

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/migrate"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/setupbootstrap"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func coreDir() string { return filepath.Join("..", "..", "migrations") }
func db(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("processing_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	cfg.MaxConns = 1
	cfg.MinConns = 0
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	admin.Close()
	t.Cleanup(func() {
		p.Close()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		a, e := pgxpool.New(c, dsn)
		if e != nil {
			t.Error(e)
			return
		}
		defer a.Close()
		if _, e = a.Exec(c, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); e != nil {
			t.Error(e)
		}
	})
	if err = migrate.Apply(ctx, p, coreDir()); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, `INSERT INTO tenants(id,name) VALUES('processing-retain','Keep');INSERT INTO sites(id,tenant_id,name) VALUES('processing-site','processing-retain','Keep');INSERT INTO gateways(id,tenant_id,site_id,serial,status) VALUES('processing-gw','processing-retain','processing-site','PROCESSING-KEEP','active');INSERT INTO devices(id,tenant_id,gateway_id,profile,name,config) VALUES('processing-dev','processing-retain','processing-gw','modbus-generic','Keep','{"port":"KEEP","slave_id":11}')`); err != nil {
		t.Fatal(err)
	}
	return p
}
func assertRetained(t *testing.T, p *pgxpool.Pool, wantLedger int) {
	t.Helper()
	var n int
	var serial, config string
	if e := p.QueryRow(t.Context(), "SELECT count(*) FROM schema_migrations").Scan(&n); e != nil || n != wantLedger {
		t.Fatal(n, e)
	}
	if e := p.QueryRow(t.Context(), `SELECT g.serial,d.config::text FROM gateways g JOIN devices d ON d.gateway_id=g.id WHERE g.id='processing-gw'`).Scan(&serial, &config); e != nil || serial != "PROCESSING-KEEP" || !strings.Contains(config, "KEEP") {
		t.Fatal(serial, config, e)
	}
}
func TestStagedCatalogBytes(t *testing.T) {
	if err := checkBytes(); err != nil {
		t.Fatal(err)
	}
}
func TestStagedInstallRetainsCoreAndOldOperatorRefuses94(t *testing.T) {
	p := db(t)
	ctx := t.Context()
	v, err := Inspect(ctx, p, coreDir())
	if err != nil || v.Status != "processing_v1_absent_core93" {
		t.Fatal(v, err)
	}
	v, err = Apply(ctx, p, coreDir())
	if err != nil || v.Status != "exact_staged_processing_v1_94" || v.RuntimeAccepted || v.ProcessingEnabled {
		t.Fatal(v, err)
	}
	assertRetained(t, p, 94)
	if _, err = setupbootstrap.InspectReviewed(ctx, p, coreDir()); err == nil {
		t.Fatal("old exact93 inspector accepted94")
	}
	if _, err = setupbootstrap.ApplyReviewed(ctx, p, coreDir()); err == nil {
		t.Fatal("old exact93 apply accepted94")
	}
	for i := 0; i < 2; i++ {
		v, err = Apply(ctx, p, coreDir())
		if err != nil || v.RuntimeAccepted || v.ProcessingEnabled {
			t.Fatal(v, err)
		}
	}
	// Normal old startup replays no94 DDL because the new SQL is not packaged
	// into the core catalog. This proves only migration-loader compatibility.
	if err = migrate.Apply(ctx, p, coreDir()); err != nil {
		t.Fatal(err)
	}
	assertRetained(t, p, 94)
	var n int
	if err = p.QueryRow(ctx, "SELECT count(*) FROM ingest_processing_v1").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}
func TestStagedLedgerFaultRollsBackAllDDL(t *testing.T) {
	p := db(t)
	ctx := t.Context()
	if _, err := p.Exec(ctx, `CREATE FUNCTION refuse_processing_ledger() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.filename='0094_ingest_processing_v1.sql' THEN RAISE EXCEPTION 'synthetic staged ledger refusal';END IF;RETURN NEW;END$$;CREATE TRIGGER refuse_processing_ledger BEFORE INSERT ON schema_migrations FOR EACH ROW EXECUTE FUNCTION refuse_processing_ledger()`); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, p, coreDir()); err == nil {
		t.Fatal("ledger fault accepted")
	}
	assertRetained(t, p, 93)
	var o *string
	if err := p.QueryRow(ctx, "SELECT to_regclass('ingest_processing_v1')::text").Scan(&o); err != nil || o != nil {
		t.Fatal(o, err)
	}
	if _, err := p.Exec(ctx, "DROP TRIGGER refuse_processing_ledger ON schema_migrations;DROP FUNCTION refuse_processing_ledger()"); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, p, coreDir()); err != nil {
		t.Fatal(err)
	}
}
func TestStagedRefusesUnrecordedUnknownAndChangedShape(t *testing.T) {
	for _, mode := range []string{"unrecorded", "unknown-ledger", "changed-core", "changed-stage", "trigger"} {
		t.Run(mode, func(t *testing.T) {
			p := db(t)
			ctx := t.Context()
			var q string
			switch mode {
			case "unrecorded":
				q = "CREATE TABLE ingest_processing_v1(n int)"
			case "unknown-ledger":
				q = "INSERT INTO schema_migrations(filename) VALUES('0094_other.sql')"
			case "changed-core":
				q = "ALTER TABLE edge_pairing_proofs ADD COLUMN tampered int"
			default:
				if _, err := Apply(ctx, p, coreDir()); err != nil {
					t.Fatal(err)
				}
				if mode == "changed-stage" {
					q = "ALTER TABLE ingest_processing_v1 ADD COLUMN tampered int"
				} else {
					q = `CREATE FUNCTION staged_noop() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW;END$$;CREATE TRIGGER staged_noop BEFORE INSERT ON ingest_processing_v1 FOR EACH ROW EXECUTE FUNCTION staged_noop()`
				}
			}
			if _, err := p.Exec(ctx, q); err != nil {
				t.Fatal(err)
			}
			if _, err := Inspect(ctx, p, coreDir()); err == nil {
				t.Fatal("unexpected inspect acceptance", mode)
			}
			if _, err := Apply(ctx, p, coreDir()); err == nil {
				t.Fatal("unexpected apply acceptance", mode)
			}
		})
	}
}
func TestStagedRefusesConcurrentClientAndCancellation(t *testing.T) {
	p := db(t)
	ctx := t.Context()
	other, err := pgx.ConnectConfig(ctx, p.Config().ConnConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(ctx, p, coreDir()); err == nil {
		t.Fatal("other client accepted")
	}
	other.Close(ctx)
	assertRetained(t, p, 93)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = Apply(cancelled, p, coreDir()); err == nil {
		t.Fatal("cancelled apply accepted")
	}
	assertRetained(t, p, 93)
}
