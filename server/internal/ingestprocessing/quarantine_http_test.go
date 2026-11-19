package ingestprocessing

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var quarantineSecret = []byte("synthetic-only-quarantine-session-key-000000")

func quarantineToken(t *testing.T, tenant, user, session, role string) string {
	t.Helper()
	c := auth.Claims{TenantID: tenant, Role: role, RegisteredClaims: jwt.RegisteredClaims{Subject: user, ID: session, IssuedAt: jwt.NewNumericDate(time.Now().Add(-time.Second)), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(quarantineSecret)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func quarantineBody(t *testing.T, f httpMTLS) string {
	t.Helper()
	leaf := sha256.Sum256(f.client.Certificate[0])
	b, err := json.Marshal(map[string]any{"gateway_id": "g", "expected_leaf_sha256": hex.EncodeToString(leaf[:]), "compared_current_identity": true})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func quarantineCall(h http.Handler, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/staged/processing-v1/quarantine-gateway", strings.NewReader(body))
	r.TLS = &tls.ConnectionState{HandshakeComplete: true}
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(quarantineDeadlineRecorder{w}, r)
	return w
}
func seedQuarantineSession(t *testing.T, x Engine) {
	t.Helper()
	exec(t, x.Pool, "INSERT INTO user_sessions(id,tenant_id,user_id) VALUES('session','a','u')")
}
func quarantineState(t *testing.T, x Engine, status string, audits int) {
	t.Helper()
	var got string
	var n, denied, revision int
	if err := x.Pool.QueryRow(t.Context(), "SELECT status FROM gateways WHERE id='g'").Scan(&got); err != nil || got != status {
		t.Fatal(got, err)
	}
	if err := x.Pool.QueryRow(t.Context(), "SELECT count(*) FROM audit_log WHERE action='staged.gateway_quarantined'").Scan(&n); err != nil || n != audits {
		t.Fatal(n, err)
	}
	if err := x.Pool.QueryRow(t.Context(), "SELECT revision,(SELECT count(*) FROM admission_denied_leaf_v1) FROM admission_denial_epoch_v1 WHERE singleton=true").Scan(&revision, &denied); err != nil || revision != 0 || denied != 0 {
		t.Fatal("global denial mutated", revision, denied, err)
	}
}
func TestQuarantineControllerDisabledAndRefusesBeforeDB(t *testing.T) {
	if h, err := NewGatewayQuarantineHTTP(false, Engine{}, nil); h != nil || !errors.Is(err, ErrDisabled) {
		t.Fatal(h, err)
	}
	if h, err := NewGatewayQuarantineHTTP(true, Engine{Enabled: true}, quarantineSecret); h != nil || err == nil {
		t.Fatal(h, err)
	}
	h, err := NewGatewayQuarantineHTTP(true, Engine{Enabled: true, Pool: &pgxpool.Pool{}}, quarantineSecret)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing", "forged-context", "wrong-key", "no-session", "expired", "no-exp", "future-iat", "plaintext-forward", "API-key", "AI-marked", "not-admin", "bad-body", "extra-tenant", "get", "wrong-path"} {
		t.Run(mode, func(t *testing.T) {
			token := quarantineToken(t, "a", "u", "session", "admin")
			body := `{"gateway_id":"g","expected_leaf_sha256":"0101010101010101010101010101010101010101010101010101010101010101","compared_current_identity":true}`
			r := httptest.NewRequest("POST", "/staged/processing-v1/quarantine-gateway", nil)
			r.TLS = &tls.ConnectionState{HandshakeComplete: true}
			switch mode {
			case "missing":
				token = ""
			case "forged-context":
				token = "fake"
				r = r.WithContext(context.WithValue(r.Context(), auth.CtxRole, "admin"))
			case "wrong-key":
				token += "broken"
			case "no-session":
				token = quarantineToken(t, "a", "u", "", "admin")
			case "not-admin":
				token = quarantineToken(t, "a", "u", "session", "viewer")
			case "expired", "no-exp", "future-iat":
				c := auth.Claims{TenantID: "a", Role: "admin", RegisteredClaims: jwt.RegisteredClaims{Subject: "u", ID: "session", IssuedAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)), ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute))}}
				if mode == "no-exp" {
					c.ExpiresAt = nil
				}
				if mode == "future-iat" {
					c.IssuedAt = jwt.NewNumericDate(time.Now().Add(time.Hour))
					c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(2 * time.Hour))
				}
				token, err = jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(quarantineSecret)
				if err != nil {
					t.Fatal(err)
				}
			case "plaintext-forward":
				r.TLS = nil
				r.Header.Set("X-Forwarded-Proto", "https")
			case "API-key":
				token = "hxk_synthetic"
			case "AI-marked":
				r = r.WithContext(auth.AsAssistant(r.Context(), "a", "u", "admin"))
			case "bad-body":
				body = "{}"
			case "extra-tenant":
				body = strings.TrimSuffix(body, "}") + `,"tenant_id":"b"}`
			case "get":
				r.Method = "GET"
			case "wrong-path":
				r.URL.Path = "/v1/gateways"
			}
			r.Body = io.NopCloser(strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+token)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(quarantineDeadlineRecorder{w}, r)
			if w.Code < 400 {
				t.Fatal(w.Code)
			}
		})
	}
}
func TestQuarantineControllerLiveAdminSessionAndBinding(t *testing.T) {
	x, _ := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	seedQuarantineSession(t, x)
	h, err := NewGatewayQuarantineHTTP(true, x, quarantineSecret)
	if err != nil {
		t.Fatal(err)
	}
	token := quarantineToken(t, "a", "u", "session", "admin")
	body := quarantineBody(t, f)
	for _, pair := range [][2]string{
		{"UPDATE users SET role='viewer' WHERE id='u'", "UPDATE users SET role='admin' WHERE id='u'"},
		{"UPDATE users SET disabled_at=now() WHERE id='u'", "UPDATE users SET disabled_at=NULL WHERE id='u'"},
		{"UPDATE users SET must_change_credentials=true WHERE id='u'", "UPDATE users SET must_change_credentials=false WHERE id='u'"},
		{"UPDATE users SET tokens_valid_after=now() WHERE id='u'", "UPDATE users SET tokens_valid_after=NULL WHERE id='u'"},
		{"UPDATE user_sessions SET revoked_at=now() WHERE id='session'", "UPDATE user_sessions SET revoked_at=NULL WHERE id='session'"},
		{"UPDATE user_sessions SET user_id='other' WHERE id='session'", "UPDATE user_sessions SET user_id='u' WHERE id='session'"},
		{"UPDATE user_sessions SET tenant_id='b' WHERE id='session'", "UPDATE user_sessions SET tenant_id='a' WHERE id='session'"},
		{"UPDATE gateways SET tenant_id='b' WHERE id='g'", "UPDATE gateways SET tenant_id='a' WHERE id='g'"},
		{"UPDATE gateways SET auth_mode='password' WHERE id='g'", "UPDATE gateways SET auth_mode='cert' WHERE id='g'"},
	} {
		exec(t, x.Pool, pair[0])
		w := quarantineCall(h, token, body)
		if w.Code < 400 {
			t.Fatal(pair[0], w.Code)
		}
		quarantineState(t, x, "active", 0)
		exec(t, x.Pool, pair[1])
	}
	exec(t, x.Pool, "INSERT INTO customers(id,tenant_id,name) VALUES('cust','a','Synthetic');INSERT INTO user_customer_scope(tenant_id,user_id,customer_id) VALUES('a','u','cust')")
	if w := quarantineCall(h, token, body); w.Code != 403 {
		t.Fatal("scoped admin accepted", w.Code)
	}
	quarantineState(t, x, "active", 0)
	exec(t, x.Pool, "DELETE FROM user_customer_scope WHERE user_id='u'")
	bad := strings.Replace(body, `"gateway_id":"g"`, `"gateway_id":"other"`, 1)
	if w := quarantineCall(h, token, bad); w.Code != 409 {
		t.Fatal(w.Code)
	}
	bad = strings.Replace(body, `"expected_leaf_sha256":"`, `"expected_leaf_sha256":"00`, 1)
	if w := quarantineCall(h, token, bad); w.Code != 400 {
		t.Fatal(w.Code)
	}
	wrongLeaf := sha256.Sum256(f.client.Certificate[0])
	bad = strings.Replace(body, hex.EncodeToString(wrongLeaf[:]), strings.Repeat("0", 63)+"1", 1)
	if w := quarantineCall(h, token, bad); w.Code != 409 {
		t.Fatal("wrong full fingerprint accepted", w.Code)
	}
	// A signed token claiming another tenant cannot select A's user or gateway.
	if w := quarantineCall(h, quarantineToken(t, "b", "u", "session", "admin"), body); w.Code != 403 {
		t.Fatal(w.Code)
	}
	quarantineState(t, x, "active", 0)
	t.Log("QUARANTINE_LIVE_SESSION_ROLE_TENANT_BINDING_REFUSAL_ZERO_WRITES_OK")
}
func TestQuarantineControllerActualHTTPSAdmissionAndNoGlobalDenial(t *testing.T) {
	x, e := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	seedQuarantineSession(t, x)
	// Deliberately reuse the leaf on a second tenant's gateway. Deny-only status
	// update must not become global leaf denial or mutate that other gateway.
	leaf := sha256.Sum256(f.client.Certificate[0])
	exec(t, x.Pool, "INSERT INTO sites(id,tenant_id,name) VALUES('sb','b','B');INSERT INTO gateways(id,tenant_id,site_id,serial,status) VALUES('gb','b','sb','B','active')")
	exec(t, x.Pool, "UPDATE gateways SET cert_fingerprint=$1 WHERE id='gb'", hex.EncodeToString(leaf[:]))
	s, err := NewGuardedAdmissionServer(t.Context(), guardedConfig(t, f, x), filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	endpoint := startAdmissionServer(t, s)
	if code, _ := durableRequest(t, f, endpoint, e); code != 200 {
		t.Fatal(code)
	}
	secret := append([]byte(nil), quarantineSecret...)
	h, err := NewGatewayQuarantineHTTP(true, x, secret)
	if err != nil {
		t.Fatal(err)
	}
	for i := range secret {
		secret[i] = 0
	}
	admin := httptest.NewTLSServer(h)
	defer admin.Close()
	call := func() (int, []byte) {
		r, err := http.NewRequestWithContext(t.Context(), "POST", admin.URL+"/staged/processing-v1/quarantine-gateway", bytes.NewBufferString(quarantineBody(t, f)))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+quarantineToken(t, "a", "u", "session", "admin"))
		resp, err := admin.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, b
	}
	if code, b := call(); code != 200 || !bytes.Contains(b, []byte(`"global_leaf_denied":false`)) {
		t.Fatal(code, string(b))
	}
	quarantineState(t, x, "revoked", 1)
	if code, b := durableRequest(t, f, endpoint, e); code != 503 || bytes.Contains(b, []byte("signature")) {
		t.Fatal(code, string(b))
	}
	if next, err := NewGuardedAdmissionServer(t.Context(), guardedConfig(t, f, x), filepath.Join("..", "..", "migrations")); next != nil || err == nil {
		t.Fatal("quarantined reconstruction accepted", err)
	}
	if code, _ := call(); code != 409 {
		t.Fatal("blind repeated decision accepted", code)
	}
	quarantineState(t, x, "revoked", 1)
	counts(t, x.Pool, e.EventID, 1)
	var status string
	if err = x.Pool.QueryRow(t.Context(), "SELECT status FROM gateways WHERE id='gb'").Scan(&status); err != nil || status != "active" {
		t.Fatal(status, err)
	}
	tx, err := x.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	if err = guardDurableLeaf(t.Context(), tx, leaf); err != nil {
		t.Fatal("globally denied shared leaf", err)
	}
	if err = guardGatewayAuthority(t.Context(), tx, Envelope{TenantID: "b", GatewayID: "gb"}, leaf); err != nil {
		t.Fatal("other tenant binding affected", err)
	}
	t.Log("QUARANTINE_ACTUAL_HTTPS_TRACKED_ADMIN_PG_MTLS_REPLAY_REFUSED_OTHER_TENANT_UNCHANGED_OK")
}

func TestQuarantineAuditFailureRollsBackStatus(t *testing.T) {
	x, _ := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	seedQuarantineSession(t, x)
	h, err := NewGatewayQuarantineHTTP(true, x, quarantineSecret)
	if err != nil {
		t.Fatal(err)
	}
	exec(t, x.Pool, `CREATE FUNCTION refuse_quarantine_audit() RETURNS trigger AS $$ BEGIN IF NEW.action='staged.gateway_quarantined' THEN RAISE EXCEPTION 'synthetic audit refusal';END IF;RETURN NEW;END $$ LANGUAGE plpgsql;CREATE TRIGGER refuse_quarantine_audit BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION refuse_quarantine_audit()`)
	if w := quarantineCall(h, quarantineToken(t, "a", "u", "session", "admin"), quarantineBody(t, f)); w.Code != 503 {
		t.Fatal(w.Code)
	}
	quarantineState(t, x, "active", 0)
	t.Log("QUARANTINE_AUDIT_REFUSAL_ROLLS_BACK_GATEWAY_STATUS_OK")
}
func waitQuarantineSQL(t *testing.T, x Engine, query string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var n int
		err := x.Pool.QueryRow(t.Context(), query).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no SQL waiter", query)
		}
		time.Sleep(time.Millisecond)
	}
}
func TestQuarantineOldAdminSnapshotRefusesAfterDemotion(t *testing.T) {
	x, _ := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	seedQuarantineSession(t, x)
	h, err := NewGatewayQuarantineHTTP(true, x, quarantineSecret)
	if err != nil {
		t.Fatal(err)
	}
	block, err := x.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(block)
	if _, err = block.Exec(t.Context(), "UPDATE users SET role='viewer' WHERE id='u'"); err != nil {
		t.Fatal(err)
	}
	token, body := quarantineToken(t, "a", "u", "session", "admin"), quarantineBody(t, f)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- quarantineCall(h, token, body) }()
	waitQuarantineSQL(t, x, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'SELECT role,disabled_at%'")
	if err = block.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if w := <-done; w.Code != 403 {
		t.Fatal("old admin snapshot committed", w.Code)
	}
	quarantineState(t, x, "active", 0)
	t.Log("QUARANTINE_OLD_ADMIN_SNAPSHOT_REFUSED_AFTER_DEMOTION_OK")
}
func TestQuarantineEnteredDecisionSerializesBeforeSessionAndScopeChanges(t *testing.T) {
	x, _ := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	seedQuarantineSession(t, x)
	exec(t, x.Pool, "INSERT INTO customers(id,tenant_id,name) VALUES('cust','a','Synthetic')")
	h, err := NewGatewayQuarantineHTTP(true, x, quarantineSecret)
	if err != nil {
		t.Fatal(err)
	}
	exec(t, x.Pool, `CREATE FUNCTION hold_quarantine_audit() RETURNS trigger AS $$ BEGIN IF NEW.action='staged.gateway_quarantined' THEN PERFORM pg_advisory_xact_lock(731492068218);END IF;RETURN NEW;END $$ LANGUAGE plpgsql;CREATE TRIGGER hold_quarantine_audit BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION hold_quarantine_audit()`)
	block, err := x.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(block)
	if _, err = block.Exec(t.Context(), "SELECT pg_advisory_xact_lock(731492068218)"); err != nil {
		t.Fatal(err)
	}
	token, body := quarantineToken(t, "a", "u", "session", "admin"), quarantineBody(t, f)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- quarantineCall(h, token, body) }()
	waitQuarantineSQL(t, x, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory' AND query LIKE 'INSERT INTO audit_log%'")
	changes := make(chan error, 2)
	go func() {
		_, e := x.Pool.Exec(t.Context(), "UPDATE user_sessions SET revoked_at=now() WHERE id='session'")
		changes <- e
	}()
	go func() {
		_, e := x.Pool.Exec(t.Context(), "INSERT INTO user_customer_scope(tenant_id,user_id,customer_id) VALUES('a','u','cust')")
		changes <- e
	}()
	waitQuarantineSQL(t, x, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'UPDATE user_sessions%'")
	waitQuarantineSQL(t, x, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'INSERT INTO user_customer_scope%'")
	select {
	case err := <-changes:
		t.Fatal("changed authority before decision ended", err)
	default:
	}
	if err = block.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if w := <-done; w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for i := 0; i < 2; i++ {
		if err = <-changes; err != nil {
			t.Fatal(err)
		}
	}
	quarantineState(t, x, "revoked", 1)
	t.Log("QUARANTINE_ENTERED_COMMIT_PRECEDES_SESSION_SCOPE_CHANGES_NOT_RESPONSE_DRAIN_OK")
}
func TestQuarantineBusyScopeFenceRefusesWithoutWrite(t *testing.T) {
	x, _ := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	seedQuarantineSession(t, x)
	h, err := NewGatewayQuarantineHTTP(true, x, quarantineSecret)
	if err != nil {
		t.Fatal(err)
	}
	block, err := x.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(block)
	if _, err = block.Exec(t.Context(), "LOCK TABLE user_customer_scope IN ROW EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	if w := quarantineCall(h, quarantineToken(t, "a", "u", "session", "admin"), quarantineBody(t, f)); w.Code != 503 {
		t.Fatal(w.Code)
	}
	quarantineState(t, x, "active", 0)
	t.Log("QUARANTINE_BUSY_SCOPE_FENCE_FAILS_WITHOUT_AUTHORITY_WRITES_OK")
}

func TestQuarantineSessionExpiryCancelsEnteredAuditTransaction(t *testing.T) {
	x, _ := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	seedQuarantineSession(t, x)
	h, err := NewGatewayQuarantineHTTP(true, x, quarantineSecret)
	if err != nil {
		t.Fatal(err)
	}
	exec(t, x.Pool, `CREATE FUNCTION hold_expiring_quarantine_audit() RETURNS trigger AS $$ BEGIN PERFORM pg_advisory_xact_lock(731492068219);RETURN NEW;END $$ LANGUAGE plpgsql;CREATE TRIGGER hold_expiring_quarantine_audit BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION hold_expiring_quarantine_audit()`)
	block, err := x.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(block)
	if _, err = block.Exec(t.Context(), "SELECT pg_advisory_xact_lock(731492068219)"); err != nil {
		t.Fatal(err)
	}
	c := auth.Claims{TenantID: "a", Role: "admin", RegisteredClaims: jwt.RegisteredClaims{Subject: "u", ID: "session", IssuedAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)), ExpiresAt: jwt.NewNumericDate(time.Now().Add(2 * time.Second))}}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(quarantineSecret)
	if err != nil {
		t.Fatal(err)
	}
	body := quarantineBody(t, f)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- quarantineCall(h, token, body) }()
	waitQuarantineSQL(t, x, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory' AND query LIKE 'INSERT INTO audit_log%'")
	select {
	case w := <-done:
		if w.Code != 503 {
			t.Fatal(w.Code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("expired session still holding transaction")
	}
	if err = block.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	quarantineState(t, x, "active", 0)
	t.Log("QUARANTINE_SESSION_EXPIRY_CANCELS_ENTERED_TX_STATUS_AUDIT_ROLLBACK_OK")
}

// Explicit synthetic deadline-capable writer for unit/PG tests only. Actual
// connection deadline and saturation evidence uses real HTTPS below.
type quarantineDeadlineRecorder struct{ *httptest.ResponseRecorder }

func (quarantineDeadlineRecorder) SetReadDeadline(time.Time) error  { return nil }
func (quarantineDeadlineRecorder) SetWriteDeadline(time.Time) error { return nil }
func TestQuarantineTransportUnsupportedWriterAndStrictRequest(t *testing.T) {
	h, err := NewGatewayQuarantineHTTP(true, Engine{Enabled: true, Pool: &pgxpool.Pool{}}, quarantineSecret)
	if err != nil {
		t.Fatal(err)
	}
	f := httpTLSFixture(t)
	for _, mode := range []string{"unsupported-writer", "query", "force-query", "raw-path", "fragment", "http2", "duplicate-auth", "duplicate-type", "encoding", "oversize", "handshake"} {
		t.Run(mode, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/staged/processing-v1/quarantine-gateway", strings.NewReader(quarantineBody(t, f)))
			r.TLS = &tls.ConnectionState{HandshakeComplete: true}
			r.Header.Set("Authorization", "Bearer "+quarantineToken(t, "a", "u", "session", "admin"))
			r.Header.Set("Content-Type", "application/json")
			switch mode {
			case "query":
				r.URL.RawQuery = "a=b"
			case "force-query":
				r.URL.ForceQuery = true
			case "raw-path":
				r.URL.RawPath = "/staged/processing-v1/%71uarantine-gateway"
			case "fragment":
				r.URL.Fragment = "x"
			case "http2":
				r.ProtoMajor = 2
			case "duplicate-auth":
				r.Header.Add("Authorization", r.Header.Get("Authorization"))
			case "duplicate-type":
				r.Header.Add("Content-Type", "application/json")
			case "encoding":
				r.Header.Set("Content-Encoding", "gzip")
			case "oversize":
				r.ContentLength = 2049
			case "handshake":
				r.TLS.HandshakeComplete = false
			}
			w := httptest.NewRecorder()
			if mode == "unsupported-writer" {
				h.ServeHTTP(w, r)
			} else {
				h.ServeHTTP(quarantineDeadlineRecorder{w}, r)
			}
			if w.Code < 400 {
				t.Fatal(w.Code)
			}
		})
	}
}
func TestQuarantineActualTLSSlowBodyBoundedBeforeDB(t *testing.T) {
	h, err := NewGatewayQuarantineHTTP(true, Engine{Enabled: true, Pool: &pgxpool.Pool{}}, quarantineSecret)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewTLSServer(h)
	defer s.Close()
	conn, err := tls.Dial("tcp", strings.TrimPrefix(s.URL, "https://"), s.Client().Transport.(*http.Transport).TLSClientConfig.Clone())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err = conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = fmt.Fprintf(conn, "POST /staged/processing-v1/quarantine-gateway HTTP/1.1\r\nHost: localhost\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{", quarantineToken(t, "a", "u", "session", "admin"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 || time.Since(start) > 4500*time.Millisecond {
		t.Fatal(resp.StatusCode, time.Since(start))
	}
	t.Log("QUARANTINE_ACTUAL_TLS_SLOW_BODY_DEADLINE_NO_DB_OK")
}
func TestQuarantineActualHTTPSCapacityRefusalBeforeDB(t *testing.T) {
	x, _ := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	seedQuarantineSession(t, x)
	h, err := NewGatewayQuarantineHTTP(true, x, quarantineSecret)
	if err != nil {
		t.Fatal(err)
	}
	block, err := x.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(block)
	if _, err = block.Exec(t.Context(), "UPDATE users SET role='viewer' WHERE id='u'"); err != nil {
		t.Fatal(err)
	}
	s := httptest.NewTLSServer(h)
	defer s.Close()
	client := s.Client()
	client.Timeout = 8 * time.Second
	token, body := quarantineToken(t, "a", "u", "session", "admin"), quarantineBody(t, f)
	call := func() (int, error) {
		r, e := http.NewRequestWithContext(t.Context(), "POST", s.URL+"/staged/processing-v1/quarantine-gateway", strings.NewReader(body))
		if e != nil {
			return 0, e
		}
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		resp, e := client.Do(r)
		if e != nil {
			return 0, e
		}
		defer resp.Body.Close()
		_, e = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, e
	}
	type outcome struct {
		code int
		err  error
	}
	done := make(chan outcome, 4)
	for i := 0; i < 4; i++ {
		go func() { c, e := call(); done <- outcome{c, e} }()
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		var n int
		err = x.Pool.QueryRow(t.Context(), "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'SELECT role,disabled_at%'").Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		if n == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not four real DB waiters", n)
		}
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	if c, e := call(); c != 503 || e != nil || time.Since(start) > time.Second {
		t.Fatal("excess request queued", c, e, time.Since(start))
	}
	if err = block.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		o := <-done
		if o.err != nil || o.code != 403 {
			t.Fatal(o)
		}
	}
	quarantineState(t, x, "active", 0)
	exec(t, x.Pool, "UPDATE users SET role='admin' WHERE id='u'")
	if c, e := call(); c != 200 || e != nil {
		t.Fatal("capacity not released", c, e)
	}
	quarantineState(t, x, "revoked", 1)
	t.Log("QUARANTINE_ACTUAL_HTTPS_FOUR_SLOT_FIFTH_REFUSAL_NO_DB_QUEUE_RECOVERY_OK")
}
