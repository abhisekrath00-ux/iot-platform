package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
	"github.com/jackc/pgx/v5"
)

// Optional actual-browser boundary proof. No application worker, CA, broker,
// provider or device runs. Every database is new and removed at test close.
func TestIntegrationBrowserLostSetupAndPairingResponses(t *testing.T) {
	if os.Getenv("TEST_RECOVERY_BROWSER") != "1" {
		t.Skip("explicit local scripts/test-recovery-browser.py browser acceptance required")
	}
	cfg, err := pgx.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal("invalid local PG config")
	}
	ip := net.ParseIP(cfg.Host)
	if ip == nil || !ip.IsLoopback() {
		t.Fatal("numeric loopback PostgreSQL required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Second)
	defer cancel()
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	name := fmt.Sprintf("hx_browser_recovery_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error("scratch DB cleanup", err)
		}
	}()
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	st, err := store.Connect(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var actual string
	if err = st.Pool.QueryRow(ctx, "SELECT current_database()").Scan(&actual); err != nil || actual != name {
		t.Fatal("scratch DB identity mismatch")
	}
	t.Setenv("MIGRATIONS_DIR", "../../migrations")
	if err = migrate(ctx, st); err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	s := &server{st: st, secret: secret}
	t.Setenv("FLEET_SIGNING_KEY", base64.StdEncoding.EncodeToString(secret))
	var acceptedPublications atomic.Int32
	s.opsPublishHook = func(_ string, b []byte) error {
		acceptedPublications.Add(1)
		var envelope configPushEnvelope
		if err := json.Unmarshal(b, &envelope); err != nil {
			return err
		}
		// No broker here. The independent compiled/mTLS test covers real transport.
		// Age this durable marker to prove browser GET cannot silently expire it.
		if _, err := st.Pool.Exec(ctx, `UPDATE gateway_config_pushes SET created_at=now()-interval '1 day' WHERE id=$1`, envelope.PushID); err != nil {
			return err
		}
		return fmt.Errorf("synthetic acceptance followed by acknowledgement loss")
	}
	const tenant = "browser-recovery-local"
	seed(t, s, tenant)
	queries := []string{
		`UPDATE gateways SET cert_fingerprint='synthetic-retained-fingerprint' WHERE id='browser-recovery-local-gw'`,
		`INSERT INTO commissioning_sessions(id,tenant_id,site_id,gateway_id,serial,created_by,state,device_id) VALUES('configured-session','browser-recovery-local','browser-recovery-local-site','browser-recovery-local-gw','SER-browser-recovery-local','test-user','claimed','browser-recovery-local-dev')`,
		`INSERT INTO edge_identity_migrations(id,tenant_id,gateway_id,serial,csr_pem,csr_sha256,key_sha256) VALUES('pending-pairing','browser-recovery-local','browser-recovery-local-gw','SER-browser-recovery-local','synthetic-not-a-real-csr','synthetic-csr','synthetic-key')`,
	}
	for _, q := range queries {
		if _, err = st.Pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	sid := s.startSession(httptest.NewRequest("GET", "/", nil), tenant, "test-user")
	if sid == "" {
		t.Fatal("tracked session missing")
	}
	token, err := s.sessionToken(tenant, "admin", "test-user", sid)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/me", s.me)
	mux.HandleFunc("GET /v1/gateways/{id}/config-push", s.listConfigPushes)
	mux.HandleFunc("POST /v1/gateways/{id}/config-push", s.pushGatewayConfig)
	mux.HandleFunc("GET /v1/gateways/{id}/source-readiness", s.sourceReadiness)
	mux.HandleFunc("GET /v1/commissioning/sessions", s.listCommissionSessions)
	mux.HandleFunc("POST /v1/gateways/{id}/source-setup", s.createExistingSourceSession)
	mux.HandleFunc("GET /v1/edge/identity-migrations", s.listIdentityMigrations)
	mux.HandleFunc("POST /v1/edge/identity-migrations/{id}/revoke", s.revokePendingIdentityMigration)
	protected := auth.Middleware(secret)(s.activeUser(s.customerScope(mux)))
	var mu sync.Mutex
	var paths []string
	var postCodes []int
	// The fault boundary observes the REAL completed handler/database result, then
	// closes the connection before status/headers/body reach the real browser.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.Method == "POST" {
			rec := httptest.NewRecorder()
			protected.ServeHTTP(rec, r)
			mu.Lock()
			postCodes = append(postCodes, rec.Code)
			mu.Unlock()
			c, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				c.Close()
			}
			return
		}
		protected.ServeHTTP(w, r)
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	// Independently check actual middleware instead of injecting request context.
	read := func(tok, path string) int {
		r, _ := http.NewRequestWithContext(ctx, "GET", server.URL+path, nil)
		if tok != "" {
			r.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if read("", "/v1/commissioning/sessions") != 401 {
		t.Fatal("missing token accepted")
	}
	if _, err = st.Pool.Exec(ctx, `UPDATE users SET role='viewer' WHERE id='test-user'`); err != nil {
		t.Fatal(err)
	}
	if read(token, "/v1/commissioning/sessions") != 403 {
		t.Fatal("live role demotion ignored")
	}
	if _, err = st.Pool.Exec(ctx, `UPDATE users SET role='admin' WHERE id='test-user'`); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `UPDATE user_sessions SET revoked_at=now() WHERE id=$1`, sid); err != nil {
		t.Fatal(err)
	}
	if read(token, "/v1/edge/identity-migrations") != 401 {
		t.Fatal("tracked revocation ignored")
	}
	if _, err = st.Pool.Exec(ctx, `UPDATE user_sessions SET revoked_at=NULL WHERE id=$1`, sid); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `INSERT INTO customers(id,tenant_id,name) VALUES('browser-customer','browser-recovery-local','Synthetic scope'); INSERT INTO user_customer_scope(tenant_id,user_id,customer_id) VALUES('browser-recovery-local','test-user','browser-customer')`); err != nil {
		t.Fatal(err)
	}
	if read(token, "/v1/commissioning/sessions") != 403 || read(token, "/v1/edge/identity-migrations") != 403 {
		t.Fatal("customer scope ignored")
	}
	if _, err = st.Pool.Exec(ctx, `DELETE FROM user_customer_scope WHERE tenant_id='browser-recovery-local' AND user_id='test-user'`); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(t.TempDir(), "private-browser-config.json")
	b, err := json.Marshal(map[string]string{"endpoint": server.URL, "token": token, "root": root, "artifacts": os.Getenv("TEST_UI_ARTIFACT_DIR")})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(config, b, 0600); err != nil {
		t.Fatal(err)
	}
	python := os.Getenv("TEST_RECOVERY_PYTHON")
	if python == "" {
		python = "python3"
	}
	cmd := exec.CommandContext(ctx, python, filepath.Join(root, "scripts/test-recovery-browser.py"), "--browser-child", config)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser child failed: %v\n%s", err, out)
	}
	var browserResult struct {
		RecoveredHref           string `json:"recovered_href"`
		PairingObserved         string `json:"pairing_observed"`
		UnknownResponseRetained bool   `json:"unknown_response_retained"`
	}
	if json.Unmarshal(out, &browserResult) != nil || browserResult.PairingObserved != "revoked" || !browserResult.UnknownResponseRetained {
		t.Fatal("invalid browser result")
	}
	var recoveredID string
	if err = st.Pool.QueryRow(ctx, `SELECT id FROM commissioning_sessions WHERE tenant_id='browser-recovery-local' AND device_id IS NULL`).Scan(&recoveredID); err != nil || browserResult.RecoveredHref != "/onboarding?session="+recoveredID {
		t.Fatal("browser did not recover exact actual committed session", err)
	}
	t.Log("Actual browser observed exact committed session and revoked pending pairing; no mocked API responses.")
	mu.Lock()
	defer mu.Unlock()
	if len(postCodes) != 3 || postCodes[0] != 201 || postCodes[1] != 200 || postCodes[2] != 502 {
		t.Fatal("actual lost response commits", postCodes)
	}
	postPaths := []string{}
	for _, path := range paths {
		if strings.HasPrefix(path, "POST ") {
			postPaths = append(postPaths, path)
		}
	}
	if len(postPaths) != 3 || postPaths[2] != "POST /v1/gateways/browser-recovery-local-gw/config-push" || postPaths[0] != "POST /v1/gateways/browser-recovery-local-gw/source-setup" || postPaths[1] != "POST /v1/edge/identity-migrations/pending-pairing/revoke" {
		t.Fatal("unexpected mutation request count or destination", postPaths)
	}
	var sessions, gateways, claims, audits, idempotency int
	var state, fingerprint string
	for _, q := range []struct {
		sql string
		n   *int
	}{
		{`SELECT count(*) FROM commissioning_sessions WHERE tenant_id='browser-recovery-local'`, &sessions},
		{`SELECT count(*) FROM gateways WHERE tenant_id='browser-recovery-local'`, &gateways},
		{`SELECT count(*) FROM enrollment_tokens WHERE tenant_id='browser-recovery-local'`, &claims},
		{`SELECT count(*) FROM audit_log WHERE tenant_id='browser-recovery-local' AND action IN ('commission.source.start','edge.migration.revoke')`, &audits},
		{`SELECT count(*) FROM idempotency_keys WHERE tenant_id='browser-recovery-local' AND action='gateway.source-session' AND status='completed'`, &idempotency},
	} {
		if err = st.Pool.QueryRow(ctx, q.sql).Scan(q.n); err != nil {
			t.Fatal(err)
		}
	}
	if err = st.Pool.QueryRow(ctx, `SELECT state FROM edge_identity_migrations WHERE id='pending-pairing'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `SELECT cert_fingerprint FROM gateways WHERE id='browser-recovery-local-gw'`).Scan(&fingerprint); err != nil {
		t.Fatal(err)
	}
	if sessions != 2 || gateways != 1 || claims != 0 || audits != 2 || idempotency != 1 || state != "revoked" || fingerprint != "synthetic-retained-fingerprint" {
		t.Fatal("unexpected durable state", sessions, gateways, claims, audits, idempotency, state, fingerprint)
	}
	var configPushes int
	var pushState, pushDetail string
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM gateway_config_pushes WHERE tenant_id=$1`, tenant).Scan(&configPushes); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT status,detail FROM gateway_config_pushes WHERE tenant_id=$1`, tenant).Scan(&pushState, &pushDetail); err != nil {
		t.Fatal(err)
	}
	if acceptedPublications.Load() != 1 || configPushes != 1 || pushState != "sent" || pushDetail != configPushPublicationUnknown {
		t.Fatal("durable Apply quarantine", acceptedPublications.Load(), configPushes, pushState, pushDetail)
	}
	t.Log("ACTUAL_CHROMIUM_REACT_AUTH_TRACKED_SESSION_PG_SETUP_PAIRING_LOST_RESPONSE_RECOVERY_AND_APPLY_PUBLISH_UNKNOWN_QUARANTINE_REMOUNT_NO_RESEND_OK")
}
