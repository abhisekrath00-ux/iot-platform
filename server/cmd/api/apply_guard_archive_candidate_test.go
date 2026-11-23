package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Actual PostgreSQL archive tools, disposable schemas/databases only. This does
// not run a product backup script, restore an owner database, or accept old images.
// Explicit opt-in avoids silently using a mismatched client against CI Postgres.
func TestIntegrationApplyGuardArchiveCandidate(t *testing.T) {
	if os.Getenv("TEST_PG_ARCHIVE_TOOLS") != "1" {
		t.Skip("set TEST_PG_ARCHIVE_TOOLS=1 with matching PostgreSQL client/server")
	}
	dump, de := exec.LookPath("pg_dump")
	restore, re := exec.LookPath("pg_restore")
	if de != nil || re != nil {
		t.Fatal("explicit archive test requires matching PostgreSQL archive tools")
	}
	for _, mode := range []string{"guarded-known", "guarded-unknown", "pre-publication-snapshot", "pre-unknown-snapshot", "table-only-atomic-refusal", "table-only-partial-open-gap", "full-database-known", "full-database-unknown", "pre-full-database-snapshot"} {
		t.Run(mode, func(t *testing.T) {
			s, m, tenant, path := applyGuardFixture(t)
			if err := installApplyGuard(t, s); err != nil {
				t.Fatal(err)
			}
			var schema string
			if err := s.st.Pool.QueryRow(t.Context(), "SELECT current_schema()").Scan(&schema); err != nil {
				t.Fatal(err)
			}
			quoted := pgx.Identifier{schema}.Sanitize()
			fullDatabase := strings.Contains(mode, "full-database")
			inspectPool := s.st.Pool
			restoreURL := os.Getenv("TEST_DATABASE_URL")
			if fullDatabase {
				db := schema + "_restore"
				if _, err := s.st.Pool.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{db}.Sanitize()); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if _, err := s.st.Pool.Exec(ctx, "DROP DATABASE "+pgx.Identifier{db}.Sanitize()+" WITH (FORCE)"); err != nil {
						t.Error(err)
					}
				})
				cfg := s.st.Pool.Config()
				cfg.ConnConfig.Database = db
				u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
				if err != nil {
					t.Fatal(err)
				}
				u.Path = "/" + db
				restoreURL = u.String()
				inspectPool, err = pgxpool.NewWithConfig(t.Context(), cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer inspectPool.Close()
			}
			catalogPool := s.st.Pool
			catalog := func() (bool, bool, bool) {
				var function, trigger, index bool
				err := catalogPool.QueryRow(t.Context(), `SELECT
                    to_regprocedure($1) IS NOT NULL,
                    EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass($2) AND tgname='apply_attempt_guard_candidate' AND tgenabled='O'),
                    EXISTS(SELECT 1 FROM pg_index WHERE indexrelid=to_regclass($3) AND indisunique AND indisvalid)`,
					schema+".apply_attempt_guard_candidate()", schema+".gateway_config_pushes", schema+".apply_open_attempt_candidate").Scan(&function, &trigger, &index)
				if err != nil {
					t.Fatal(err)
				}
				return function, trigger, index
			}
			f0, t0, i0 := catalog()
			if !f0 || !t0 || !i0 {
				t.Fatal("source guard incomplete")
			}
			definition := func(pool *pgxpool.Pool) string {
				var value string
				if err := pool.QueryRow(t.Context(), `SELECT pg_get_functiondef(to_regprocedure($1)) || pg_get_triggerdef(t.oid) || pg_get_indexdef(to_regclass($3)) FROM pg_trigger t WHERE t.tgrelid=to_regclass($2) AND t.tgname='apply_attempt_guard_candidate'`, schema+".apply_attempt_guard_candidate()", schema+".gateway_config_pushes", schema+".apply_open_attempt_candidate").Scan(&value); err != nil {
					t.Fatal(err)
				}
				return value
			}
			definitionBefore := definition(s.st.Pool)
			var rowBefore string
			archive := filepath.Join(t.TempDir(), "guard.dump")
			var calls atomic.Int32
			publish := func(string, []byte) error {
				calls.Add(1)
				if mode == "guarded-unknown" || mode == "pre-unknown-snapshot" || mode == "full-database-unknown" {
					return errors.New("synthetic acknowledgement unavailable")
				}
				return nil
			}
			s.opsPublishHook = publish
			snapshot := func() {
				args := []string{"--dbname", os.Getenv("TEST_DATABASE_URL"), "--format=custom", "--file", archive}
				if strings.HasPrefix(mode, "table-only") {
					args = append(args, "--table", schema+".gateway_config_pushes")
				} else if !fullDatabase {
					args = append(args, "--schema", schema)
				}
				if out, err := exec.CommandContext(t.Context(), dump, args...).CombinedOutput(); err != nil {
					t.Fatalf("dump: %v: %s", err, out)
				}
			}
			if strings.HasPrefix(mode, "pre-") {
				snapshot()
			}
			first := call(m, tenant, "admin", "POST", path, `{}`)
			wantFirst := 202
			if mode == "guarded-unknown" || mode == "pre-unknown-snapshot" || mode == "full-database-unknown" {
				wantFirst = 502
			}
			if first.Code != wantFirst {
				t.Fatal(first.Code, first.Body.String())
			}
			if _, err := s.st.Pool.Exec(t.Context(), `UPDATE gateway_config_pushes SET created_at=now()-interval '1 day' WHERE tenant_id=$1`, tenant); err != nil {
				t.Fatal(err)
			}
			if err := s.st.Pool.QueryRow(t.Context(), `SELECT row_to_json(p)::text FROM gateway_config_pushes p WHERE tenant_id=$1`, tenant).Scan(&rowBefore); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(mode, "pre-") {
				snapshot()
			}
			if out, err := exec.CommandContext(t.Context(), restore, "--list", archive).CombinedOutput(); err != nil {
				t.Fatalf("archive listing: %v: %s", err, out)
			}
			// Drop only the private fixture. The real product schema is never replaced.
			if !fullDatabase {
				if _, err := s.st.Pool.Exec(t.Context(), "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasPrefix(mode, "table-only") {
				if _, err := s.st.Pool.Exec(t.Context(), "CREATE SCHEMA "+quoted); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"--dbname", restoreURL, "--no-owner"}
			if mode != "table-only-partial-open-gap" {
				args = append(args, "--exit-on-error", "--single-transaction")
			}
			args = append(args, archive)
			out, err := exec.CommandContext(t.Context(), restore, args...).CombinedOutput()
			if strings.HasPrefix(mode, "table-only") {
				if err == nil || !strings.Contains(string(out), "apply_attempt_guard_candidate") {
					t.Fatalf("expected missing function restore refusal: %v: %s", err, out)
				}
			} else if err != nil {
				t.Fatalf("restore: %v: %s", err, out)
			}
			if mode == "table-only-atomic-refusal" {
				var exists bool
				if err := s.st.Pool.QueryRow(t.Context(), "SELECT to_regclass($1) IS NOT NULL", schema+".gateway_config_pushes").Scan(&exists); err != nil || exists {
					t.Fatal("failed atomic restore left table", exists, err)
				}
				t.Log("ACTUAL_TABLE_ONLY_ARCHIVE_MISSING_FUNCTION_ATOMIC_RESTORE_REFUSED_WITH_NO_TABLE; listing alone is insufficient")
				return
			}
			catalogPool = inspectPool
			f1, t1, i1 := catalog()
			if mode == "table-only-partial-open-gap" {
				if f1 || t1 || !i1 {
					t.Fatal("unexpected partial restore catalog", f1, t1, i1)
				}
			} else {
				if !f1 || !t1 || !i1 {
					t.Fatal("restored guard incomplete", f1, t1, i1)
				}
				if definition(inspectPool) != definitionBefore {
					t.Fatal("restore changed exact guard function/trigger/index definitions")
				}
			}
			var rows int
			if err := inspectPool.QueryRow(t.Context(), "SELECT count(*) FROM gateway_config_pushes WHERE tenant_id=$1", tenant).Scan(&rows); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(mode, "pre-") {
				if rows != 0 {
					t.Fatal("snapshot unexpectedly retained later attempt", rows)
				}
			} else {
				var rowAfter string
				if err := inspectPool.QueryRow(t.Context(), `SELECT row_to_json(p)::text FROM gateway_config_pushes p WHERE tenant_id=$1`, tenant).Scan(&rowAfter); err != nil {
					t.Fatal(err)
				}
				if rows != 1 || rowBefore != rowAfter {
					t.Fatal("archive changed original attempt contents", rows)
				}
			}
			// New connection/pool discards prepared statements and process-local state.
			cfg := inspectPool.Config()
			pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			fresh := &server{st: &store.Store{Pool: pool}, opsPublishHook: publish}
			mux := http.NewServeMux()
			mux.HandleFunc("POST /v1/gateways/{id}/config-push", fresh.pushGatewayConfig)
			mux.HandleFunc("GET /v1/gateways/{id}/config-push", fresh.listConfigPushes)
			seen := call(mux, tenant, "admin", "GET", path, "")
			next := call(mux, tenant, "admin", "POST", path, `{}`)
			if mode == "guarded-known" || mode == "guarded-unknown" || mode == "full-database-known" || mode == "full-database-unknown" {
				if seen.Code != 200 || !strings.Contains(seen.Body.String(), `"status":"sent"`) || next.Code != 409 || calls.Load() != 1 {
					t.Fatal("restored guard rearmed", seen.Code, seen.Body.String(), next.Code, calls.Load())
				}
				t.Logf("ACTUAL_ARCHIVE_%s_RESTORES_FUNCTION_TRIGGER_INDEX_AND_EXACT_UNRESOLVED_ROW_GET_SENT_POST409_ONE_FAKE_ACCEPTANCE; not product script/image acceptance", mode)
			} else {
				wantNext := 202
				if mode == "pre-unknown-snapshot" || mode == "full-database-unknown" {
					wantNext = 502
				}
				if seen.Code != 200 || next.Code != wantNext || calls.Load() != 2 {
					t.Fatal("expected restore OPEN gap", seen.Code, seen.Body.String(), next.Code, calls.Load())
				}
				if mode == "table-only-partial-open-gap" && !strings.Contains(seen.Body.String(), `"status":"expired"`) {
					t.Fatal("expected restored row expiry", seen.Body.String())
				}
				t.Logf("OPEN_GAP_%s_ACTUAL_ARCHIVE_RESTORE_GET200_SECOND_PUBLISH_TWO_FAKE_ACCEPTANCES; no real delivery", mode)
			}
			if mode == "guarded-known" || mode == "guarded-unknown" || mode == "full-database-known" || mode == "full-database-unknown" {
				if _, err := pool.Exec(t.Context(), `DELETE FROM gateway_config_pushes WHERE tenant_id=$1`, tenant); err == nil {
					t.Fatal("restored trigger permits open delete")
				}
				if _, err := pool.Exec(t.Context(), `UPDATE gateway_config_pushes SET sha256='changed-after-restore' WHERE tenant_id=$1`, tenant); err == nil {
					t.Fatal("restored trigger permits content change")
				}
				if _, err := pool.Exec(t.Context(), `INSERT INTO gateway_config_pushes(id,tenant_id,gateway_id,version,devices_yaml,sha256,status,requested_by) VALUES('restored-next',$1,$2,99,'devices: []','synthetic','sent','test-user')`, tenant, tenant+"-gw"); err == nil {
					t.Fatal("restored index permits second open row")
				}
			}
			assertGuardLedger(t, fresh)
		})
	}
}
