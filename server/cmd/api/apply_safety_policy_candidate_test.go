package main

import (
	"errors"
	"fmt"
	"github.com/eclipse/paho.mqtt.golang/packets"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/applyadmission"
)

func TestIntegrationApplySafetyPolicyCandidate(t *testing.T) {
	for _, mode := range []string{"guarded-known", "guarded-unknown", "missing", "disabled-trigger", "changed-function", "missing-index", "changed-predicate"} {
		t.Run(mode, func(t *testing.T) {
			s, m, tenant, path := applyGuardFixture(t)
			s.applySafetyCandidate = true
			s.applyAttemptFence = testApplyFence(t)
			if mode != "missing" {
				if err := installApplyGuard(t, s); err != nil {
					t.Fatal(err)
				}
			}
			mutation := map[string]string{
				"disabled-trigger":  "ALTER TABLE gateway_config_pushes DISABLE TRIGGER apply_attempt_guard_candidate",
				"changed-function":  "CREATE OR REPLACE FUNCTION apply_attempt_guard_candidate() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$",
				"missing-index":     "DROP INDEX apply_open_attempt_candidate",
				"changed-predicate": "DROP INDEX apply_open_attempt_candidate; CREATE UNIQUE INDEX apply_open_attempt_candidate ON gateway_config_pushes(tenant_id,gateway_id) WHERE status='sent'",
			}[mode]
			if mutation != "" {
				if _, err := s.st.Pool.Exec(t.Context(), mutation); err != nil {
					t.Fatal(err)
				}
			}
			var calls atomic.Int32
			s.opsPublishHook = func(string, []byte) error {
				calls.Add(1)
				if mode == "guarded-unknown" {
					return errors.New("synthetic lost acknowledgement")
				}
				return nil
			}
			first := call(m, tenant, "admin", "POST", path, `{}`)
			if !strings.HasPrefix(mode, "guarded-") {
				var n int
				if err := s.st.Pool.QueryRow(t.Context(), "SELECT count(*) FROM gateway_config_pushes").Scan(&n); err != nil {
					t.Fatal(err)
				}
				if first.Code != 503 || calls.Load() != 0 || n != 0 {
					t.Fatal("unsafe guard admitted publication", first.Code, first.Body.String(), calls.Load(), n)
				}
				return
			}
			want := 202
			if mode == "guarded-unknown" {
				want = 502
			}
			if first.Code != want || calls.Load() != 1 {
				t.Fatal(first.Code, first.Body.String(), calls.Load())
			}
			if _, err := s.st.Pool.Exec(t.Context(), "UPDATE gateway_config_pushes SET created_at=now()-interval '1 day'"); err != nil {
				t.Fatal(err)
			}
			seen := call(m, tenant, "admin", "GET", path, "")
			next := call(m, tenant, "admin", "POST", path, `{}`)
			if seen.Code != 200 || !strings.Contains(seen.Body.String(), `"status":"sent"`) || next.Code != 409 || calls.Load() != 1 {
				t.Fatal(seen.Code, seen.Body.String(), next.Code, calls.Load())
			}
			tx, err := s.st.Pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(t.Context())
			if err := applyadmission.Verify(t.Context(), tx); err != nil {
				t.Fatal(err)
			}
			assertGuardLedger(t, s)
		})
	}
}

// No publisher hook: exercise the policy-selected production candidate on TCP.
// The local protocol peer is not a verified TLS broker or a gateway executor.
func TestIntegrationApplySafetyPolicyDirectPublisher(t *testing.T) {
	for _, ack := range []bool{true, false} {
		t.Run(fmt.Sprint("ack-", ack), func(t *testing.T) {
			s, m, tenant, path := applyGuardFixture(t)
			if err := installApplyGuard(t, s); err != nil {
				t.Fatal(err)
			}
			s.applySafetyCandidate = true
			s.applyAttemptFence = testApplyFence(t)
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			done := make(chan struct{})
			var connections, publications atomic.Int32
			go func() {
				defer close(done)
				for {
					c, err := l.Accept()
					if err != nil {
						return
					}
					connections.Add(1)
					c.SetDeadline(time.Now().Add(2 * time.Second))
					if _, err = packets.ReadPacket(c); err != nil {
						c.Close()
						continue
					}
					packets.NewControlPacket(packets.Connack).Write(c)
					p, err := packets.ReadPacket(c)
					if err == nil {
						if pub, ok := p.(*packets.PublishPacket); ok {
							publications.Add(1)
							if ack {
								reply := packets.NewControlPacket(packets.Puback).(*packets.PubackPacket)
								reply.MessageID = pub.MessageID
								reply.Write(c)
							}
						}
					}
					c.Close()
				}
			}()
			t.Setenv("MQTT_HOST", "127.0.0.1")
			t.Setenv("MQTT_PORT", fmt.Sprint(l.Addr().(*net.TCPAddr).Port))
			t.Setenv("MQTT_TLS", "false")
			for _, key := range []string{"MQTT_CA_FILE", "MQTT_CERT_FILE", "MQTT_KEY_FILE"} {
				t.Setenv(key, "")
			}
			first := call(m, tenant, "admin", "POST", path, `{}`)
			want := 202
			if !ack {
				want = 502
			}
			if first.Code != want {
				t.Fatal(first.Code, first.Body.String())
			}
			if _, err := s.st.Pool.Exec(t.Context(), "UPDATE gateway_config_pushes SET created_at=now()-interval '1 day'"); err != nil {
				t.Fatal(err)
			}
			seen := call(m, tenant, "admin", "GET", path, "")
			next := call(m, tenant, "admin", "POST", path, `{}`)
			l.Close()
			<-done
			if seen.Code != 200 || !strings.Contains(seen.Body.String(), `"status":"sent"`) || next.Code != 409 || connections.Load() != 1 || publications.Load() != 1 {
				t.Fatal("rearmed integrated policy", seen.Code, seen.Body.String(), next.Code, connections.Load(), publications.Load())
			}
		})
	}
}

func TestIntegrationApplySafetyCandidateRefusesReplicaSession(t *testing.T) {
	s, _, _, _ := applyGuardFixture(t)
	if err := installApplyGuard(t, s); err != nil {
		t.Fatal(err)
	}
	tx, err := s.st.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err := tx.Exec(t.Context(), "SET LOCAL session_replication_role=replica"); err != nil {
		t.Fatal(err)
	}
	if err := applyadmission.Verify(t.Context(), tx); err == nil {
		t.Fatal("disabled normal triggers accepted")
	}
}

func testApplyFence(t *testing.T) *applyadmission.Fence {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := applyadmission.OpenFence(dir)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestIntegrationApplySafetyRetainedFenceAcrossHistoryLoss(t *testing.T) {
	for _, mode := range []string{"known", "unknown", "missing-storage"} {
		t.Run(mode, func(t *testing.T) {
			s, m, tenant, path := applyGuardFixture(t)
			if err := installApplyGuard(t, s); err != nil {
				t.Fatal(err)
			}
			s.applySafetyCandidate = true
			if mode != "missing-storage" {
				s.applyAttemptFence = testApplyFence(t)
			}
			var calls atomic.Int32
			s.opsPublishHook = func(string, []byte) error {
				calls.Add(1)
				if mode == "unknown" {
					return errors.New("synthetic unknown")
				}
				return nil
			}
			first := call(m, tenant, "admin", "POST", path, `{}`)
			if mode == "missing-storage" {
				if first.Code != 409 || calls.Load() != 0 {
					t.Fatal(first.Code, calls.Load())
				}
				return
			}
			want := 202
			if mode == "unknown" {
				want = 502
			}
			if first.Code != want {
				t.Fatal(first.Code, first.Body.String())
			}
			// Model only the later-attempt history absent after stale database restore.
			// Actual archive coverage separately verifies the external fence retained.
			if _, err := s.st.Pool.Exec(t.Context(), `ALTER TABLE gateway_config_pushes DISABLE TRIGGER apply_attempt_guard_candidate; DELETE FROM gateway_config_pushes; ALTER TABLE gateway_config_pushes ENABLE TRIGGER apply_attempt_guard_candidate`); err != nil {
				t.Fatal(err)
			}
			seen := call(m, tenant, "admin", "GET", path, "")
			if seen.Code != 200 || !strings.Contains(seen.Body.String(), `"recovery_quarantined":true`) {
				t.Fatal(seen.Code, seen.Body.String())
			}
			next := call(m, tenant, "admin", "POST", path, `{}`)
			if next.Code != 409 || calls.Load() != 1 {
				t.Fatal("lost DB history rearmed", next.Code, next.Body.String(), calls.Load())
			}
			var rows int
			if err := s.st.Pool.QueryRow(t.Context(), "SELECT count(*) FROM gateway_config_pushes").Scan(&rows); err != nil || rows != 0 {
				t.Fatal(rows, err)
			}
		})
	}
}

func TestIntegrationApplySafetyFenceBeforeCommitFailure(t *testing.T) {
	s, m, tenant, path := applyGuardFixture(t)
	if err := installApplyGuard(t, s); err != nil {
		t.Fatal(err)
	}
	s.applySafetyCandidate = true
	s.applyAttemptFence = testApplyFence(t)
	// Fail only at transaction commit, after the retained fence has been synced.
	if _, err := s.st.Pool.Exec(t.Context(), `CREATE FUNCTION fail_apply_commit_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic deferred commit failure'; END $$; CREATE CONSTRAINT TRIGGER fail_apply_commit_fixture AFTER INSERT ON gateway_config_pushes DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_apply_commit_fixture()`); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	s.opsPublishHook = func(string, []byte) error { calls.Add(1); return nil }
	first := call(m, tenant, "admin", "POST", path, `{}`)
	if first.Code != 500 || calls.Load() != 0 {
		t.Fatal(first.Code, first.Body.String(), calls.Load())
	}
	if _, err := s.st.Pool.Exec(t.Context(), `DROP TRIGGER fail_apply_commit_fixture ON gateway_config_pushes; DROP FUNCTION fail_apply_commit_fixture()`); err != nil {
		t.Fatal(err)
	}
	next := call(m, tenant, "admin", "POST", path, `{}`)
	if next.Code != 409 || calls.Load() != 0 {
		t.Fatal("commit failure rearmed", next.Code, next.Body.String(), calls.Load())
	}
	var n int
	if err := s.st.Pool.QueryRow(t.Context(), "SELECT count(*) FROM gateway_config_pushes").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestIntegrationApplySafetyFenceReadStatus(t *testing.T) {
	s, m, tenant, path := applyGuardFixture(t)
	if err := installApplyGuard(t, s); err != nil {
		t.Fatal(err)
	}
	s.applySafetyCandidate = true
	missing := call(m, tenant, "admin", "GET", path, "")
	if missing.Code != 503 {
		t.Fatal(missing.Code, missing.Body.String())
	}
	s.applyAttemptFence = testApplyFence(t)
	empty := call(m, tenant, "admin", "GET", path, "")
	if empty.Code != 200 || strings.TrimSpace(empty.Body.String()) != "[]" {
		t.Fatal(empty.Code, empty.Body.String())
	}
	var calls atomic.Int32
	s.opsPublishHook = func(string, []byte) error { calls.Add(1); return nil }
	first := call(m, tenant, "admin", "POST", path, `{}`)
	if first.Code != 202 {
		t.Fatal(first.Code, first.Body.String())
	}
	if _, err := s.st.Pool.Exec(t.Context(), `UPDATE gateway_config_pushes SET status='rejected'`); err != nil {
		t.Fatal(err)
	}
	terminal := call(m, tenant, "admin", "GET", path, "")
	next := call(m, tenant, "admin", "POST", path, `{}`)
	if terminal.Code != 200 || strings.Contains(terminal.Body.String(), `"recovery_quarantined":true`) || !strings.Contains(terminal.Body.String(), `"status":"rejected"`) || next.Code != 202 || calls.Load() != 2 {
		t.Fatal(terminal.Code, terminal.Body.String(), next.Code, calls.Load())
	}
	// Default-off path and its wire response remain unchanged.
	s.applySafetyCandidate = false
	legacy := call(m, tenant, "admin", "GET", path, "")
	if legacy.Code != 200 || strings.Contains(legacy.Body.String(), "recovery_quarantined") {
		t.Fatal(legacy.Code, legacy.Body.String())
	}
}

func TestIntegrationApplySafetyTerminalProgressionStaleHistory(t *testing.T) {
	for _, status := range []string{"confirmed", "rejected", "rolled_back"} {
		t.Run(status, func(t *testing.T) {
			s, m, tenant, path := applyGuardFixture(t)
			if err := installApplyGuard(t, s); err != nil {
				t.Fatal(err)
			}
			s.applySafetyCandidate = true
			s.applyAttemptFence = testApplyFence(t)
			var calls atomic.Int32
			s.opsPublishHook = func(string, []byte) error { calls.Add(1); return nil }
			first := call(m, tenant, "admin", "POST", path, `{}`)
			if first.Code != 202 {
				t.Fatal(first.Code, first.Body.String())
			}
			if status != "rejected" {
				if _, err := s.st.Pool.Exec(t.Context(), `UPDATE gateway_config_pushes SET status='applied'`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.st.Pool.Exec(t.Context(), `UPDATE gateway_config_pushes SET status=$1`, status); err != nil {
				t.Fatal(err)
			}
			second := call(m, tenant, "admin", "POST", path, `{}`)
			if second.Code != 202 || calls.Load() != 2 {
				t.Fatal(second.Code, second.Body.String(), calls.Load())
			}
			latest, err := s.applyAttemptFence.Latest(tenant, tenant+"-gw")
			if err != nil || latest.Version != 2 || latest.Previous == "" {
				t.Fatal(latest, err)
			}
			// Exact older terminal DB receipt survives, later unresolved row is lost.
			if _, err := s.st.Pool.Exec(t.Context(), `ALTER TABLE gateway_config_pushes DISABLE TRIGGER apply_attempt_guard_candidate; DELETE FROM gateway_config_pushes WHERE version=2; ALTER TABLE gateway_config_pushes ENABLE TRIGGER apply_attempt_guard_candidate`); err != nil {
				t.Fatal(err)
			}
			seen := call(m, tenant, "admin", "GET", path, "")
			next := call(m, tenant, "admin", "POST", path, `{}`)
			if seen.Code != 200 || !strings.Contains(seen.Body.String(), `"recovery_quarantined":true`) || next.Code != 409 || calls.Load() != 2 {
				t.Fatal(seen.Code, seen.Body.String(), next.Code, calls.Load())
			}
		})
	}
}

func TestIntegrationApplySafetyTerminalIdentityMismatch(t *testing.T) {
	for _, mode := range []string{"id", "sha256", "version", "no-fence"} {
		t.Run(mode, func(t *testing.T) {
			s, m, tenant, path := applyGuardFixture(t)
			if err := installApplyGuard(t, s); err != nil {
				t.Fatal(err)
			}
			s.applySafetyCandidate = true
			s.applyAttemptFence = testApplyFence(t)
			var calls atomic.Int32
			s.opsPublishHook = func(string, []byte) error { calls.Add(1); return nil }
			if r := call(m, tenant, "admin", "POST", path, `{}`); r.Code != 202 {
				t.Fatal(r.Code, r.Body.String())
			}
			if _, err := s.st.Pool.Exec(t.Context(), `UPDATE gateway_config_pushes SET status='rejected'`); err != nil {
				t.Fatal(err)
			}
			if mode == "no-fence" {
				s.applyAttemptFence = testApplyFence(t)
			} else {
				mutation := map[string]string{"id": "id='mismatched'", "sha256": "sha256='wrong-digest'", "version": "version=9"}[mode]
				if _, err := s.st.Pool.Exec(t.Context(), `ALTER TABLE gateway_config_pushes DISABLE TRIGGER apply_attempt_guard_candidate; UPDATE gateway_config_pushes SET `+mutation+`; ALTER TABLE gateway_config_pushes ENABLE TRIGGER apply_attempt_guard_candidate`); err != nil {
					t.Fatal(err)
				}
			}
			r := call(m, tenant, "admin", "POST", path, `{}`)
			if r.Code != 409 || calls.Load() != 1 {
				t.Fatal(r.Code, r.Body.String(), calls.Load())
			}
		})
	}
}

func TestIntegrationApplySafetyMissingRetainedHistoryIsVisible(t *testing.T) {
	s, m, tenant, path := applyGuardFixture(t)
	if err := installApplyGuard(t, s); err != nil {
		t.Fatal(err)
	}
	s.applySafetyCandidate = true
	s.applyAttemptFence = testApplyFence(t)
	s.opsPublishHook = func(string, []byte) error { return nil }
	if r := call(m, tenant, "admin", "POST", path, `{}`); r.Code != 202 {
		t.Fatal(r.Code, r.Body.String())
	}
	if _, err := s.st.Pool.Exec(t.Context(), `UPDATE gateway_config_pushes SET status='rejected'`); err != nil {
		t.Fatal(err)
	}
	s.applyAttemptFence = testApplyFence(t)
	seen := call(m, tenant, "admin", "GET", path, "")
	if seen.Code != 200 || !strings.Contains(seen.Body.String(), `"recovery_quarantined":true`) {
		t.Fatal("missing retained chain hidden by terminal history", seen.Code, seen.Body.String())
	}
}
