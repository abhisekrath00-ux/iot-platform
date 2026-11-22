package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Only this disposable schema receives candidate SQL. Existing product tables,
// startup migrations, updater ledger and default routes never install the guard.
func applyGuardFixture(t *testing.T) (*server, *http.ServeMux, string, string) {
	t.Helper()
	return applyGuardFixtureTenant(t, "itest-apply-guard-"+uuid.NewString())
}
func applyGuardFixtureTenant(t *testing.T, tenant string) (*server, *http.ServeMux, string, string) {
	t.Helper()
	root, _ := testServer(t)
	name := "apply_guard_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := root.st.Pool.Exec(t.Context(), "CREATE SCHEMA "+quoted+"; CREATE TABLE "+quoted+".gateway_config_pushes (LIKE public.gateway_config_pushes INCLUDING ALL)"); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = quoted + ",public"
	cfg.MaxConns = 12
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := root.st.Pool.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	s := &server{st: &store.Store{Pool: pool}}
	seed(t, s, tenant)
	t.Setenv("FLEET_SIGNING_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	m := http.NewServeMux()
	m.HandleFunc("POST /v1/gateways/{id}/config-push", s.pushGatewayConfig)
	m.HandleFunc("GET /v1/gateways/{id}/config-push", s.listConfigPushes)
	return s, m, tenant, "/v1/gateways/" + tenant + "-gw/config-push"
}
func installApplyGuard(t *testing.T, s *server) error {
	t.Helper()
	b, err := os.ReadFile("../../internal/applyadmission/candidate.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.st.Pool.Begin(t.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(t.Context(), string(b)); err != nil {
		return err
	}
	return tx.Commit(t.Context())
}
func insertApplyGuardRow(t *testing.T, s *server, tenant, id, status string) error {
	t.Helper()
	_, err := s.st.Pool.Exec(t.Context(), `INSERT INTO gateway_config_pushes(id,tenant_id,gateway_id,version,devices_yaml,sha256,status,requested_by) VALUES($1,$2,$3,1,'devices: []','synthetic',$4,'test-user')`, id, tenant, tenant+"-gw", status)
	return err
}
func assertGuardLedger(t *testing.T, s *server) {
	t.Helper()
	var n int
	if err := s.st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM public.schema_migrations`).Scan(&n); err != nil || n != 93 {
		t.Fatal("candidate changed migration ledger", n, err)
	}
}

func TestIntegrationApplyGuardCandidateKnownPublication(t *testing.T) {
	s, m, tenant, path := applyGuardFixture(t)
	if err := installApplyGuard(t, s); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	s.opsPublishHook = func(string, []byte) error { calls.Add(1); return nil }
	first := call(m, tenant, "admin", "POST", path, `{}`)
	if first.Code != 202 {
		t.Fatal(first.Code, first.Body.String())
	}
	if _, err := s.st.Pool.Exec(t.Context(), `UPDATE gateway_config_pushes SET created_at=now()-interval '1 day' WHERE tenant_id=$1`, tenant); err != nil {
		t.Fatal(err)
	}
	// Fresh pool proves the guard is database state, not a Go in-memory lock.
	cfg := s.st.Pool.Config()
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fresh := &server{st: &store.Store{Pool: pool}, opsPublishHook: s.opsPublishHook}
	again := http.NewServeMux()
	again.HandleFunc("POST /v1/gateways/{id}/config-push", fresh.pushGatewayConfig)
	again.HandleFunc("GET /v1/gateways/{id}/config-push", fresh.listConfigPushes)
	seen := call(again, tenant, "admin", "GET", path, "")
	next := call(again, tenant, "admin", "POST", path, `{}`)
	if seen.Code != 200 || !strings.Contains(seen.Body.String(), `"status":"sent"`) || next.Code != 409 || calls.Load() != 1 {
		t.Fatal("known publication rearmed", seen.Code, seen.Body.String(), next.Code, calls.Load())
	}
	assertGuardLedger(t, s)
	t.Log("CANDIDATE_ONLY_DB_GUARD_KNOWN_PUBLICATION_DAYOLD_NEW_POOL_GET_SENT_POST409_ONE_FAKE_ACCEPTANCE; no real delivery, activation or old-image acceptance")
}

func TestIntegrationApplyGuardCandidatePublishError(t *testing.T) {
	s, m, tenant, path := applyGuardFixture(t)
	if err := installApplyGuard(t, s); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	s.opsPublishHook = func(string, []byte) error {
		calls.Add(1)
		return errors.New("synthetic accepted then acknowledgement unavailable")
	}
	first := call(m, tenant, "admin", "POST", path, `{}`)
	if _, err := s.st.Pool.Exec(t.Context(), `UPDATE gateway_config_pushes SET created_at=now()-interval '1 day' WHERE tenant_id=$1`, tenant); err != nil {
		t.Fatal(err)
	}
	seen := call(m, tenant, "admin", "GET", path, "")
	next := call(m, tenant, "admin", "POST", path, `{}`)
	if first.Code != 502 || seen.Code != 200 || !strings.Contains(seen.Body.String(), `"status":"sent"`) || next.Code != 409 || calls.Load() != 1 {
		t.Fatal("uncertain publication rearmed", first.Code, seen.Body.String(), next.Code, calls.Load())
	}
	t.Log("CANDIDATE_ONLY_DB_GUARD_ERROR_EXPIRY_GET_SENT_POST409_ONE_FAKE_ACCEPTANCE; legacy HTTP error wording can remain misleading, not real delivery")
}

func TestIntegrationApplyGuardCandidateTransitions(t *testing.T) {
	for _, terminal := range []string{"confirmed", "rolled_back", "rejected"} {
		t.Run(terminal, func(t *testing.T) {
			s, _, tenant, _ := applyGuardFixture(t)
			if err := installApplyGuard(t, s); err != nil {
				t.Fatal(err)
			}
			if err := insertApplyGuardRow(t, s, tenant, "first", "sent"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.st.Pool.Exec(t.Context(), `DELETE FROM gateway_config_pushes WHERE id='first'`); err == nil {
				t.Fatal("open row deleted")
			}
			if _, err := s.st.Pool.Exec(t.Context(), `UPDATE gateway_config_pushes SET sha256='mutated' WHERE id='first'`); err == nil {
				t.Fatal("approved content changed")
			}
			if _, err := s.st.Pool.Exec(t.Context(), `UPDATE gateway_config_pushes SET tenant_id='foreign',gateway_id='foreign' WHERE id='first'`); err == nil {
				t.Fatal("identity moved")
			}
			if _, err := s.st.Pool.Exec(t.Context(), `UPDATE gateway_config_pushes SET status='confirmed' WHERE id='first'`); err == nil {
				t.Fatal("receipt phase skipped")
			}
			// Match existing ingest predecessor/tenant/gateway predicates, without
			// pretending these SQL fixtures authenticate a real gateway receipt.
			target := "applied"
			if terminal == "rejected" {
				target = terminal
			}
			tag, err := s.st.Pool.Exec(t.Context(), `UPDATE gateway_config_pushes SET status=$1 WHERE id='first' AND tenant_id=$2 AND gateway_id=$3 AND status='sent'`, target, tenant, tenant+"-gw")
			if err != nil || tag.RowsAffected() != 1 {
				t.Fatal(err)
			}
			if terminal != "rejected" {
				if _, err := s.st.Pool.Exec(t.Context(), `UPDATE gateway_config_pushes SET status='expired' WHERE id='first'`); err == nil {
					t.Fatal("applied attempt expired")
				}
				if _, err := s.st.Pool.Exec(t.Context(), `INSERT INTO gateway_config_pushes(id,tenant_id,gateway_id,version,devices_yaml,sha256,status,requested_by) VALUES('second',$1,$2,2,'devices: []','second','sent','test-user')`, tenant, tenant+"-gw"); err == nil {
					t.Fatal("applied attempt rearmed")
				}
				if _, err := s.st.Pool.Exec(t.Context(), `UPDATE gateway_config_pushes SET status=$1 WHERE id='first' AND status='applied'`, terminal); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.st.Pool.Exec(t.Context(), `UPDATE gateway_config_pushes SET status='sent' WHERE id='first'`); err == nil {
				t.Fatal("terminal attempt reset")
			}
			// Use a fresh version, as the historical version uniqueness also remains.
			_, err = s.st.Pool.Exec(t.Context(), `INSERT INTO gateway_config_pushes(id,tenant_id,gateway_id,version,devices_yaml,sha256,status,requested_by) VALUES('next',$1,$2,2,'devices: []','next','sent','test-user')`, tenant, tenant+"-gw")
			if err != nil {
				t.Fatal("terminal release refused", err)
			}
		})
	}
}

func TestIntegrationApplyGuardCandidateAtomicAdoptionRefusal(t *testing.T) {
	for _, kind := range []string{"failed", "expired", "duplicate-open", "ddl-function-collision", "ddl-function-collision-autocommit"} {
		t.Run(kind, func(t *testing.T) {
			s, _, tenant, _ := applyGuardFixture(t)
			status := kind
			if kind == "duplicate-open" || strings.HasPrefix(kind, "ddl-function-collision") {
				status = "sent"
			}
			if err := insertApplyGuardRow(t, s, tenant, "legacy", status); err != nil {
				t.Fatal(err)
			}
			if kind == "duplicate-open" {
				if _, err := s.st.Pool.Exec(t.Context(), `INSERT INTO gateway_config_pushes(id,tenant_id,gateway_id,version,devices_yaml,sha256,status,requested_by) VALUES('legacy2',$1,$2,2,'devices: []','old','sent','test-user')`, tenant, tenant+"-gw"); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasPrefix(kind, "ddl-function-collision") {
				if _, err := s.st.Pool.Exec(t.Context(), `CREATE FUNCTION apply_attempt_guard_candidate() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$`); err != nil {
					t.Fatal(err)
				}
			}
			var adoptionErr error
			if strings.HasSuffix(kind, "-autocommit") {
				sql, err := os.ReadFile("../../internal/applyadmission/candidate.sql")
				if err != nil {
					t.Fatal(err)
				}
				_, adoptionErr = s.st.Pool.Exec(t.Context(), string(sql))
			} else {
				adoptionErr = installApplyGuard(t, s)
			}
			if adoptionErr == nil {
				t.Fatal("ambiguous adoption accepted")
			}
			var exists bool
			if err := s.st.Pool.QueryRow(t.Context(), `SELECT to_regclass('apply_open_attempt_candidate') IS NOT NULL`).Scan(&exists); err != nil || exists {
				t.Fatal("partial candidate installation", exists, err)
			}
			var functionExists bool
			if err := s.st.Pool.QueryRow(t.Context(), `SELECT to_regprocedure('apply_attempt_guard_candidate()') IS NOT NULL`).Scan(&functionExists); err != nil || functionExists != strings.HasPrefix(kind, "ddl-function-collision") {
				t.Fatal("function adoption state", functionExists, err)
			}
			var state string
			if err := s.st.Pool.QueryRow(t.Context(), `SELECT status FROM gateway_config_pushes WHERE id='legacy'`).Scan(&state); err != nil || state != status {
				t.Fatal("legacy history reclassified", state, err)
			}
			assertGuardLedger(t, s)
		})
	}
}

func TestIntegrationApplyGuardCandidateConcurrentInsert(t *testing.T) {
	s, _, tenant, _ := applyGuardFixture(t)
	if err := installApplyGuard(t, s); err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 1; i <= 12; i++ {
		wg.Add(1)
		go func(v int) {
			defer wg.Done()
			<-start
			_, err := s.st.Pool.Exec(t.Context(), `INSERT INTO gateway_config_pushes(id,tenant_id,gateway_id,version,devices_yaml,sha256,status,requested_by) VALUES($1,$2,$3,$4,'devices: []','race','sent','test-user')`, fmt.Sprint("race-", v), tenant, tenant+"-gw", v)
			if err == nil {
				wins.Add(1)
			} else {
				var pe *pgconn.PgError
				if !errors.As(err, &pe) || pe.Code != "23505" {
					t.Errorf("unexpected concurrent refusal: %v", err)
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("open attempt winners", wins.Load())
	}
	// Same guard permits a separate tenant/gateway, not a global serialization gate.
	if err := insertApplyGuardRow(t, s, tenant+"-other", "other", "sent"); err != nil {
		t.Fatal("foreign gateway blocked", err)
	}
	assertGuardLedger(t, s)
	t.Log("CANDIDATE_ONLY_PRIVATE_SCHEMA_12_CONCURRENT_INSERTS_ONE_WINNER_AND_DISTINCT_GATEWAY_ADMITTED; migration93 unchanged")
}

func TestIntegrationApplyGuardCandidateAbsentAndRemovedBaseline(t *testing.T) {
	for _, mode := range []string{"absent-default", "removed-after-acceptance"} {
		t.Run(mode, func(t *testing.T) {
			s, m, tenant, path := applyGuardFixture(t)
			if mode == "removed-after-acceptance" {
				if err := installApplyGuard(t, s); err != nil {
					t.Fatal(err)
				}
			}
			var calls atomic.Int32
			s.opsPublishHook = func(string, []byte) error { calls.Add(1); return nil }
			first := call(m, tenant, "admin", "POST", path, `{}`)
			if first.Code != 202 {
				t.Fatal(first.Code, first.Body.String())
			}
			if mode == "removed-after-acceptance" {
				if _, err := s.st.Pool.Exec(t.Context(), `DROP TRIGGER apply_attempt_guard_candidate ON gateway_config_pushes; DROP FUNCTION apply_attempt_guard_candidate(); DROP INDEX apply_open_attempt_candidate`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.st.Pool.Exec(t.Context(), `UPDATE gateway_config_pushes SET created_at=now()-interval '1 day' WHERE tenant_id=$1`, tenant); err != nil {
				t.Fatal(err)
			}
			seen := call(m, tenant, "admin", "GET", path, "")
			next := call(m, tenant, "admin", "POST", path, `{}`)
			if seen.Code != 200 || !strings.Contains(seen.Body.String(), `"status":"expired"`) || next.Code != 202 || calls.Load() != 2 {
				t.Fatal("expected OPEN baseline not reproduced", seen.Code, seen.Body.String(), next.Code, calls.Load())
			}
			assertGuardLedger(t, s)
			t.Logf("OPEN_GAP_%s_KNOWN_PUBLICATION_EXPIRES_REARMS_TWO_FAKE_ACCEPTANCES; same migration93 ledger does not establish guarded restore/rollback, no real delivery", mode)
		})
	}
}

func TestIntegrationApplyGuardCandidateAdoptionWriterFence(t *testing.T) {
	s, _, tenant, _ := applyGuardFixture(t)
	b, err := os.ReadFile("../../internal/applyadmission/candidate.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.st.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(t.Context(), string(b)); err != nil {
		t.Fatal(err)
	}
	writer, err := s.st.Pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Release()
	pid := writer.Conn().PgConn().PID()
	done := make(chan error, 1)
	go func() {
		_, err := writer.Exec(t.Context(), `INSERT INTO gateway_config_pushes(id,tenant_id,gateway_id,version,devices_yaml,sha256,status,requested_by) VALUES('late-legacy',$1,$2,1,'devices: []','synthetic','failed','test-user')`, tenant, tenant+"-gw")
		done <- err
	}()
	// Observe an actual database lock wait, not a fixed sleep as proof of fencing.
	waited := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err = s.st.Pool.QueryRow(t.Context(), `SELECT coalesce(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1`, pid).Scan(&waited); err != nil {
			t.Fatal(err)
		}
		if waited {
			break
		}
		select {
		case e := <-done:
			t.Fatalf("writer crossed adoption fence: %v", e)
		case <-time.After(5 * time.Millisecond):
		}
	}
	if !waited {
		t.Fatal("adoption lock wait not observed")
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != "P0001" {
			t.Fatal("late legacy write not refused by installed guard", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("writer did not settle after commit")
	}
	var n int
	if err = s.st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM gateway_config_pushes`).Scan(&n); err != nil || n != 0 {
		t.Fatal("late legacy history leaked", n, err)
	}
	t.Log("CANDIDATE_ADOPTION_ACTUAL_PG_LOCK_WAIT_THEN_COMMIT_LATE_LEGACY_WRITE_REFUSED; SQL fence only, not in-flight MQTT quiescence")
}
