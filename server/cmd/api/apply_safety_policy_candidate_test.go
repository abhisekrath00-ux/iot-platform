package main

import (
	"errors"
	"fmt"
	"github.com/eclipse/paho.mqtt.golang/packets"
	"net"
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
