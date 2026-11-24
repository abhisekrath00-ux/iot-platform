package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/applyadmission"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/mqttconfig"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Combines two unwired candidates ONLY in a disposable private schema with
// generated mTLS identities and a read-only observer, never an edge executor.
func verifyApplyGuardKnownPublicationMTLS(t *testing.T, port, ca, gc, gk, ac, ak string) {
	verifyApplyGuardPolicyMTLS(t, port, ca, gc, gk, ac, ak, false)
}
func verifyApplyGuardPolicyMTLS(t *testing.T, port, ca, gc, gk, ac, ak string, integrated bool, ingestIdentity ...string) {
	t.Helper()
	s, m, tenant, path := applyGuardFixtureTenant(t, "itest-mqpipe")
	if err := installApplyGuard(t, s); err != nil {
		t.Fatal(err)
	}
	options, err := mqttconfig.Options(func(k string) string {
		return map[string]string{"MQTT_TLS": "true", "MQTT_HOST": "127.0.0.1", "MQTT_PORT": port, "MQTT_CA_FILE": ca, "MQTT_CERT_FILE": gc, "MQTT_KEY_FILE": gk}[k]
	}, "apply-guard-known-ack-observer")
	if err != nil {
		t.Fatal(err)
	}
	observer := mqtt.NewClient(options)
	tok := observer.Connect()
	if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatal("connect", tok.Error())
	}
	defer observer.Disconnect(100)
	var deliveries, calls atomic.Int32
	seen := make(chan configPushEnvelope, 4)
	topic := "t/itest-mqpipe/g/itest-mqpipe-gw/config"
	tok = observer.Subscribe(topic, 1, func(_ mqtt.Client, msg mqtt.Message) {
		var e configPushEnvelope
		if json.Unmarshal(msg.Payload(), &e) == nil && e.PushID != "" {
			deliveries.Add(1)
			select {
			case seen <- e:
			default:
			}
		}
	})
	if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatal("subscribe", tok.Error())
	}
	for k, v := range map[string]string{"MQTT_TLS": "true", "MQTT_HOST": "127.0.0.1", "MQTT_PORT": port, "MQTT_CA_FILE": ca, "MQTT_CERT_FILE": ac, "MQTT_KEY_FILE": ak} {
		t.Setenv(k, v)
	}
	s.opsPublishHook = func(got string, b []byte) error {
		calls.Add(1)
		if got != topic {
			t.Errorf("wrong topic: %s", got)
		}
		return publishApplyMQTTOnceCandidate(t.Context(), got, b)
	}
	bindingDirectory, bindingPath := "", ""
	if integrated {
		base := t.TempDir()
		if err := os.Chmod(base, 0700); err != nil {
			t.Fatal(err)
		}
		bindingDirectory = filepath.Join(base, "retained")
		bindingPath = filepath.Join(base, "expected.json")
		if err := os.Mkdir(bindingDirectory, 0700); err != nil {
			t.Fatal(err)
		}
		if err := applyadmission.PrepareNewDeploymentBinding(bindingDirectory, bindingPath, "disposable-mtls"); err != nil {
			t.Fatal(err)
		}
		s.applyAttemptFence, err = applyadmission.OpenDeploymentFence(bindingDirectory, bindingPath, "disposable-mtls")
		if err != nil {
			t.Fatal(err)
		}
		tx, err := s.st.Pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if err := applyadmission.PreflightNewInstall(t.Context(), tx, s.applyAttemptFence); err != nil {
			t.Fatal(err)
		}
		tx.Rollback(t.Context())
		s.applySafetyCandidate = true
		s.opsPublishHook = nil
	}
	first := call(m, tenant, "admin", "POST", path, `{}`)
	if first.Code != 202 {
		t.Fatal("actual broker ACK/handler", first.Code, first.Body.String())
	}
	var e configPushEnvelope
	select {
	case e = <-seen:
	case <-time.After(3 * time.Second):
		t.Fatal("signed envelope not observed")
	}
	verifyIndependentlyObservedApply(t, e, tenant)
	if _, err := s.st.Pool.Exec(t.Context(), `UPDATE gateway_config_pushes SET created_at=now()-interval '1 day' WHERE id=$1`, e.PushID); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), s.st.Pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if integrated {
		s.applyAttemptFence, err = applyadmission.OpenDeploymentFence(bindingDirectory, bindingPath, "disposable-mtls")
		if err != nil {
			t.Fatal(err)
		}
	}
	fresh := &server{st: &store.Store{Pool: pool}, opsPublishHook: s.opsPublishHook, applySafetyCandidate: integrated, applyAttemptFence: s.applyAttemptFence}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/gateways/{id}/config-push", fresh.listConfigPushes)
	mux.HandleFunc("POST /v1/gateways/{id}/config-push", fresh.pushGatewayConfig)
	listed := call(mux, tenant, "admin", "GET", path, "")
	next := call(mux, tenant, "admin", "POST", path, `{}`)
	var rows []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	expectedCalls := int32(1)
	if integrated {
		expectedCalls = 0
	}
	if listed.Code != 200 || json.Unmarshal(listed.Body.Bytes(), &rows) != nil || len(rows) != 1 || rows[0].ID != e.PushID || rows[0].Status != "sent" || next.Code != 409 || calls.Load() != expectedCalls || deliveries.Load() != 1 {
		t.Fatal("known ACK rearmed", listed.Code, listed.Body.String(), next.Code, calls.Load(), deliveries.Load())
	}
	if integrated {
		// Real compiled ingest, same isolated schema, generated mTLS identity.
		// Synthetic gateway receipt, NOT physical execution or a hardware test.
		if len(ingestIdentity) != 2 {
			t.Fatal("ingest identity missing")
		}
		ic, ik := ingestIdentity[0], ingestIdentity[1]
		ingestBinary := os.Getenv("TEST_INGEST_BINARY")
		if ingestBinary == "" {
			t.Fatal("integrated receipt test needs compiled ingest")
		}
		db, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		q := db.Query()
		q.Set("search_path", s.st.Pool.Config().ConnConfig.RuntimeParams["search_path"])
		db.RawQuery = q.Encode()
		logPath := filepath.Join(t.TempDir(), "receipt-ingest.log")
		logFile, err := os.Create(logPath)
		if err != nil {
			t.Fatal(err)
		}
		defer logFile.Close()
		process := exec.Command(ingestBinary)
		process.Env = append(os.Environ(), "DATABASE_URL="+db.String(), "MQTT_TLS=true", "MQTT_HOST=127.0.0.1", "MQTT_PORT="+port, "MQTT_CA_FILE="+ca, "MQTT_CERT_FILE="+ic, "MQTT_KEY_FILE="+ik, "GOOGLE_CLIENT_ID=", "SMTP_HOST=", "SLACK_BOT_TOKEN=", "INGEST_SHARED_GROUP=", "SPARKPLUG_TENANT=")
		process.Stdout = logFile
		process.Stderr = logFile
		if err := process.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { process.Process.Kill(); process.Wait() }()
		deadline := time.Now().Add(10 * time.Second)
		for {
			b, _ := os.ReadFile(logPath)
			if strings.Contains(string(b), "ingest subscriptions ready") {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("ingest readiness", string(b))
			}
			time.Sleep(20 * time.Millisecond)
		}
		for _, state := range []string{"applied", "confirmed"} {
			payload, _ := json.Marshal(map[string]string{"push_id": e.PushID, "state": state, "detail": "synthetic verified-mTLS fixture response"})
			tok := observer.Publish(topic+"/result", 1, false, payload)
			if !tok.WaitTimeout(3*time.Second) || tok.Error() != nil {
				t.Fatal(tok.Error())
			}
			deadline = time.Now().Add(3 * time.Second)
			for {
				var status string
				err := pool.QueryRow(t.Context(), `SELECT status FROM gateway_config_pushes WHERE id=$1`, e.PushID).Scan(&status)
				if err == nil && status == state {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("actual ingest receipt not recorded", state, status, err)
				}
				time.Sleep(20 * time.Millisecond)
			}
		}
		terminal := call(mux, tenant, "admin", "GET", path, "")
		if terminal.Code != 200 || strings.Contains(terminal.Body.String(), "recovery_quarantined") || !strings.Contains(terminal.Body.String(), `"status":"confirmed"`) {
			t.Fatal(terminal.Code, terminal.Body.String())
		}
		second := call(mux, tenant, "admin", "POST", path, `{}`)
		if second.Code != 202 {
			t.Fatal(second.Code, second.Body.String())
		}
		select {
		case next := <-seen:
			verifyIndependentlyObservedApply(t, next, tenant)
			if next.PushID == e.PushID || next.Version != e.Version+1 {
				t.Fatal("not distinct new request", next)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("new signed request not observed")
		}
		blocked := call(mux, tenant, "admin", "POST", path, `{}`)
		if blocked.Code != 409 || deliveries.Load() != 2 {
			t.Fatal("new unresolved intent rearmed", blocked.Code, deliveries.Load())
		}
		t.Log("REAL_COMPILED_INGEST_VERIFIED_MTLS_SYNTHETIC_APPLIED_CONFIRMED_RECEIPT_MATCHES_RETAINED_HEAD_NEW_DISTINCT_INTENT_ONE_NEW_PUBLICATION_THIRD_POST409; no physical execution")
	}
	assertGuardLedger(t, s)
	t.Log("ISOLATED_DB_GUARD_AND_ONE_CONNECTION_CANDIDATES_REAL_MTLS_BROKER_ACK_HANDLER202_ONE_SIGNED_OBSERVER_DELIVERY_DAYOLD_NEW_POOL_GET_SENT_POST409_ONE_PUBLISH; no executor, gateway receipt, physical application or activation")
}

// Independent canonical bytes and Ed25519 verification, not the server signing
// helper. This is synthetic source content observed at a read-only subscriber.
func verifyIndependentlyObservedApply(t *testing.T, e configPushEnvelope, tenant string) {
	t.Helper()
	digest := sha256.Sum256([]byte(e.DevicesYAML))
	if e.TenantID != tenant || e.Serial != "SER-"+tenant || e.PushID == "" || e.Version < 1 || e.ApprovedBy != "test-user" || e.SHA256 != hex.EncodeToString(digest[:]) || !strings.Contains(e.DevicesYAML, "10.0.0.7") || !strings.Contains(e.DevicesYAML, "modbus-tcp") || !e.ExpiresAt.After(e.IssuedAt) || e.ExpiresAt.Sub(e.IssuedAt) > 20*time.Minute || e.IssuedAt.After(time.Now().Add(time.Minute)) || !e.ExpiresAt.After(time.Now()) {
		t.Fatal("observed envelope/content mismatch", e)
	}
	seed, err := base64.StdEncoding.DecodeString(os.Getenv("FLEET_SIGNING_KEY"))
	if err != nil || len(seed) != ed25519.SeedSize {
		t.Fatal("synthetic signing seed unavailable")
	}
	key := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	sig, err := base64.StdEncoding.DecodeString(e.Signature)
	if err != nil {
		t.Fatal(err)
	}
	canonical := strings.Join([]string{"hexmon-config-push-v1", e.TenantID, e.Serial, strconv.Itoa(e.Version), e.PushID, e.SHA256, e.ExpiresAt.UTC().Format(time.RFC3339)}, "\n")
	if !ed25519.Verify(key, []byte(canonical), sig) {
		t.Fatal("observed envelope signature invalid")
	}
}
