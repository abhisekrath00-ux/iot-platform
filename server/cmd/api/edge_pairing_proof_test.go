package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/pairingproof"
	"github.com/jackc/pgx/v5/pgxpool"
)

func proofTestServer(t *testing.T) *server {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("isolated PostgreSQL required")
	}
	pool, e := pgxpool.New(t.Context(), raw)
	if e != nil {
		t.Fatal(e)
	}
	schema := fmt.Sprintf("pairproof_%d", time.Now().UnixNano())
	if _, e = pool.Exec(t.Context(), "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); pool.Close() })
	u, e := url.Parse(raw)
	if e != nil {
		t.Fatal(e)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	t.Setenv("TEST_DATABASE_URL", u.String())
	s, _ := testServer(t)
	seed(t, s, "itest-pairproof")
	t.Setenv("BROKER_CERT_ONLY", "1")
	t.Setenv("MQTT_TLS", "true")
	t.Setenv("BROKER_ACL_FILE", filepath.Join(t.TempDir(), "acl"))
	_, e = s.st.Pool.Exec(t.Context(), `UPDATE gateways SET cert_fingerprint='fp' WHERE id='itest-pairproof-gw';
 INSERT INTO edge_identity_migrations(id,tenant_id,gateway_id,serial,csr_pem,csr_sha256,key_sha256,state,cert_fingerprint)
 VALUES('migration-proof','itest-pairproof','itest-pairproof-gw','SER-itest-pairproof','public-csr','csr','key','issued','fp')`)
	if e != nil {
		t.Fatal(e)
	}
	return s
}

func proofMux(s *server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/edge/self/identity-migrations/{id}/broker-proof", s.createPairingProof)
	mux.HandleFunc("GET /v1/edge/self/broker-proofs/{proof}", s.getPairingProof)
	return mux
}
func proofCall(h http.Handler, tenant, gateway, method, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	r.TLS = &tls.ConnectionState{}
	c := context.WithValue(r.Context(), auth.CtxTenant, tenant)
	c = context.WithValue(c, auth.CtxRole, "gateway")
	c = context.WithValue(c, auth.CtxUser, gateway)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r.WithContext(c))
	return w
}
func TestIntegrationPairingProofIsFreshOwnGatewayAndNeverTelemetry(t *testing.T) {
	s := proofTestServer(t)
	mux := proofMux(s)
	create := func() map[string]any {
		w := proofCall(mux, "itest-pairproof", "itest-pairproof-gw", "POST", "/v1/edge/self/identity-migrations/migration-proof/broker-proof")
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body)
		}
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	out := create()
	id, nonce, topic := out["id"].(string), out["nonce"].(string), out["topic"].(string)
	path := "/v1/edge/self/broker-proofs/" + id
	if w := proofCall(mux, "itest-pairproof", "itest-pairproof-gw", "GET", path); w.Code != 200 || !strings.Contains(w.Body.String(), "waiting_for_broker_observation") {
		t.Fatal(w.Code, w.Body)
	}
	payload, _ := json.Marshal(map[string]string{"id": id, "nonce": nonce})
	for _, badtopic := range []string{"t/foreign/g/itest-pairproof-gw/pairing/proof", "t/itest-pairproof/g/foreign/pairing/proof", "t/itest-pairproof/g/itest-pairproof-gw/config"} {
		if ok, _ := pairingproof.Observe(t.Context(), s.st.Pool, badtopic, payload); ok {
			t.Fatal("foreign topic accepted")
		}
	}
	wrong, _ := json.Marshal(map[string]string{"id": id, "nonce": strings.Repeat("a", 64)})
	if ok, _ := pairingproof.Observe(t.Context(), s.st.Pool, topic, wrong); ok {
		t.Fatal("wrong nonce accepted")
	}
	if ok, e := pairingproof.Observe(t.Context(), s.st.Pool, topic, payload); e != nil || !ok {
		t.Fatal(ok, e)
	}
	if ok, e := pairingproof.Observe(t.Context(), s.st.Pool, topic, payload); e != nil || ok {
		t.Fatal("replay changed receipt", ok, e)
	}
	if w := proofCall(mux, "itest-pairproof", "itest-pairproof-gw", "GET", path); w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"observed"`) {
		t.Fatal(w.Code, w.Body)
	}
	if w := proofCall(mux, "foreign", "itest-pairproof-gw", "GET", path); w.Code != 404 {
		t.Fatal("foreign receipt", w.Code)
	}
	// A replacement challenge invalidates the old receipt, even if it was observed.
	replacement := create()
	if replacement["id"] == id {
		t.Fatal("challenge reused")
	}
	if w := proofCall(mux, "itest-pairproof", "itest-pairproof-gw", "GET", path); w.Code != 404 {
		t.Fatal("old receipt reused")
	}
	if ok, _ := pairingproof.Observe(t.Context(), s.st.Pool, topic, payload); ok {
		t.Fatal("old nonce accepted")
	}
	var telemetry int
	if e := s.st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM telemetry WHERE tenant_id='itest-pairproof'`).Scan(&telemetry); e != nil || telemetry != 5 {
		t.Fatal("proof changed measured readings", telemetry, e)
	}
	// Lab mode cannot make or read proof evidence.
	t.Setenv("BROKER_CERT_ONLY", "")
	if w := proofCall(mux, "itest-pairproof", "itest-pairproof-gw", "POST", "/v1/edge/self/identity-migrations/migration-proof/broker-proof"); w.Code != 409 {
		t.Fatal("lab proof accepted", w.Code)
	}
}
func TestIntegrationPairingProofRejectsExpiryAndChangedIdentity(t *testing.T) {
	s := proofTestServer(t)
	mux := proofMux(s)
	for _, change := range []string{"expiry", "certificate", "serial", "revoked"} {
		_, e := s.st.Pool.Exec(t.Context(), `UPDATE gateways SET status='active',serial='SER-itest-pairproof',cert_fingerprint='fp' WHERE id='itest-pairproof-gw'`)
		if e != nil {
			t.Fatal(e)
		}
		w := proofCall(mux, "itest-pairproof", "itest-pairproof-gw", "POST", "/v1/edge/self/identity-migrations/migration-proof/broker-proof")
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body)
		}
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		payload, _ := json.Marshal(map[string]any{"id": out["id"], "nonce": out["nonce"]})
		q := map[string]string{"expiry": `UPDATE edge_pairing_proofs SET expires_at=now()-interval '1 second'`, "certificate": `UPDATE gateways SET cert_fingerprint='changed' WHERE id='itest-pairproof-gw'`, "serial": `UPDATE gateways SET serial='changed' WHERE id='itest-pairproof-gw'`, "revoked": `UPDATE gateways SET status='revoked' WHERE id='itest-pairproof-gw'`}[change]
		if _, e = s.st.Pool.Exec(t.Context(), q); e != nil {
			t.Fatal(e)
		}
		if ok, e := pairingproof.Observe(t.Context(), s.st.Pool, out["topic"].(string), payload); e != nil || ok {
			t.Fatal(change, ok, e)
		}
		w = proofCall(mux, "itest-pairproof", "itest-pairproof-gw", "GET", "/v1/edge/self/broker-proofs/"+out["id"].(string))
		if w.Code == 200 {
			t.Fatal(change, "stale receipt returned")
		}
	}
}
