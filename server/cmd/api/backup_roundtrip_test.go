package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
	"github.com/jackc/pgx/v5"
)

// Actual scripts + matching PostgreSQL bytes, with only Docker transport shimmed.
// Explicit local opt-in; both databases are new and owned by this test. This is
// not clean-host/container recovery, secrets/files restoration or measured RTO.
func TestIntegrationFullBackupRoundtrip(t *testing.T) {
	if os.Getenv("TEST_FULL_BACKUP_ROUNDTRIP") != "1" {
		t.Skip("TEST_FULL_BACKUP_ROUNDTRIP=1 required for disposable local databases")
	}
	cfg, err := pgx.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal("test database configuration unavailable")
	}
	ip := net.ParseIP(cfg.Host)
	if ip == nil || !ip.IsLoopback() {
		t.Fatal("roundtrip requires numeric loopback PostgreSQL")
	}
	clients := map[string]string{}
	for _, name := range []string{"pg_dump", "pg_restore", "psql"} {
		p, e := exec.LookPath(name)
		if e != nil {
			t.Fatal("matching PostgreSQL clients required", name)
		}
		clients[name] = p
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal("local test PG unavailable")
	}
	defer admin.Close(context.Background())
	prefix := fmt.Sprintf("hx_roundtrip_%d_", time.Now().UnixNano())
	source, target := prefix+"source", prefix+"target"
	for _, db := range []string{source, target} {
		if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{db}.Sanitize()); err != nil {
			t.Fatal(err)
		}
		defer func(db string) {
			_, err := admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{db}.Sanitize()+" WITH (FORCE)")
			if err != nil {
				t.Error("disposable database cleanup failed", err)
			}
		}(db)
	}
	connect := func(db string) *store.Store {
		u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
		if err != nil || u.Scheme != "postgres" || u.RawQuery != "sslmode=disable" {
			t.Fatal("explicit local postgres URL with sslmode=disable required")
		}
		u.Path = "/" + db
		st, err := store.Connect(ctx, u.String())
		if err != nil {
			t.Fatal(err)
		}
		var actual string
		if err := st.Pool.QueryRow(ctx, "SELECT current_database()").Scan(&actual); err != nil || actual != db {
			st.Close()
			t.Fatal("disposable database binding mismatch")
		}
		return st
	}
	src := connect(source)
	defer src.Close()
	t.Setenv("MIGRATIONS_DIR", "../../migrations")
	if err := migrate(ctx, src); err != nil {
		t.Fatal(err)
	}
	s := &server{st: src}
	seed(t, s, "itest-backup-a")
	seed(t, s, "itest-backup-b")
	for _, query := range []string{
		`INSERT INTO device_profiles(id,tenant_id,name,driver_profile,points,created_by,connection_defaults) VALUES('roundtrip-profile','itest-backup-a','Synthetic USB meter','modbus-generic','[{"id":"voltage","register":0,"type":"float32"}]','test-user','{"baud":9600,"parity":"none","address":1}')`,
		`UPDATE gateways SET cert_fingerprint='synthetic-retained-fingerprint' WHERE id='itest-backup-a-gw'`,
		`INSERT INTO edge_serial_port_inventory(gateway_id,tenant_id,ports) VALUES('itest-backup-a-gw','itest-backup-a','[{"path":"COM7","label":"synthetic USB meter"}]')`,
		`INSERT INTO edge_identity_migrations(id,tenant_id,gateway_id,serial,csr_pem,csr_sha256,key_sha256,state,cert_pem,cert_fingerprint) VALUES('roundtrip-identity','itest-backup-a','itest-backup-a-gw','SER-itest-backup-a','synthetic-not-a-real-csr','synthetic-csr-hash','synthetic-key-hash','issued','synthetic-not-a-real-cert','synthetic-retained-fingerprint')`,
		`INSERT INTO edge_pairing_proofs(id,migration_id,tenant_id,gateway_id,cert_fingerprint,nonce_sha256) VALUES('roundtrip-proof','roundtrip-identity','itest-backup-a','itest-backup-a-gw','synthetic-retained-fingerprint','synthetic-nonce-hash')`,
	} {
		if _, err := src.Pool.Exec(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	// Sorted row JSON fingerprints include every public table (parents and leaves)
	// in this small synthetic DB. No count-only "success" can hide changed values.
	fingerprint := func(st *store.Store) map[string]string {
		rows, err := st.Pool.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename`)
		if err != nil {
			t.Fatal(err)
		}
		names := []string{}
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				t.Fatal(err)
			}
			names = append(names, n)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, n := range names {
			var v string
			q := `SELECT count(*)::text || ':' || md5(COALESCE(string_agg(j,E'\n' ORDER BY j),'')) FROM (SELECT row_to_json(x)::text j FROM ` + pgx.Identifier{"public", n}.Sanitize() + ` x) q`
			if err := st.Pool.QueryRow(ctx, q).Scan(&v); err != nil {
				t.Fatal(err)
			}
			out[n] = v
		}
		return out
	}
	before := fingerprint(src)
	if len(before) < 90 {
		t.Fatal("unexpectedly incomplete test schema")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	shim := `#!/usr/bin/env bash
set -euo pipefail
[ "$1 $2 $3 $4" = 'compose exec -T postgres' ] || exit 92
shift 4
case "$1" in pg_dump) shift;exec "$REAL_PGDUMP" "$@";;pg_restore) shift;exec "$REAL_PGRESTORE" "$@";;psql) shift;exec "$REAL_PSQL" "$@";;*) exit 93;;esac
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(shim), 0700); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "REAL_PGDUMP="+clients["pg_dump"], "REAL_PGRESTORE="+clients["pg_restore"], "REAL_PSQL="+clients["psql"], "PGHOST="+cfg.Host, fmt.Sprintf("PGPORT=%d", cfg.Port), "PGUSER="+cfg.User, "PGPASSWORD="+cfg.Password, "PGSSLMODE=disable", "POSTGRES_USER="+cfg.User)
	run := func(db, script string, args ...string) {
		cmd := exec.CommandContext(ctx, "bash", append([]string{filepath.Join(root, "scripts", script)}, args...)...)
		cmd.Env = append(env, "POSTGRES_DB="+db)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("actual %s failed: %v %s", script, err, b)
		}
	}
	backups := filepath.Join(dir, "private backups")
	run(source, "backup.sh", backups)
	archives, err := filepath.Glob(filepath.Join(backups, "*.dump.gz"))
	if err != nil || len(archives) != 1 {
		t.Fatal("backup archive missing")
	}
	run(target, "restore.sh", "--verify", archives[0])
	run(target, "restore.sh", archives[0])
	dst := connect(target)
	defer dst.Close()
	if after := fingerprint(dst); !reflect.DeepEqual(before, after) {
		t.Fatal("full-table row fingerprints differ after actual script restore")
	}
	if err := migrate(ctx, dst); err != nil {
		t.Fatal("restored migration idempotence", err)
	}
	if after := fingerprint(dst); !reflect.DeepEqual(before, after) {
		t.Fatal("idempotent migration changed restored data")
	}
	restored := &server{st: dst}
	for _, tenant := range []string{"itest-backup-a", "itest-backup-b"} {
		w := call(http.HandlerFunc(restored.latestTelemetry), tenant, "viewer", "GET", "/v1/telemetry/latest?device_id=itest-backup-a-dev", "")
		if w.Code != 200 || (tenant == "itest-backup-a" && !strings.Contains(w.Body.String(), `"value":21`)) || (tenant == "itest-backup-b" && strings.TrimSpace(w.Body.String()) != "[]") {
			t.Fatal("restored API identity/data boundary failed", w.Code, w.Body.String())
		}
	}
	t.Logf("actual backup.sh/restore.sh via Docker transport shim: %d table row fingerprints identical;93migration reapply unchanged;two tenant API reads retain boundary;source config/gateway fingerprint/identity/inventory/proof retained; no external secrets/files/container/clean-host/RTO claim", len(before))
}
