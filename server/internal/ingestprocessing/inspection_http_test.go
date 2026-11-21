package ingestprocessing

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func inspectionBody(t *testing.T, e Envelope) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"gateway_id": e.GatewayID, "event_id": e.EventID, "observed_at": e.ObservedAt.Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func inspectionCall(h http.Handler, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", inspectionPath, strings.NewReader(body))
	r.TLS = &tls.ConnectionState{HandshakeComplete: true}
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(quarantineDeadlineRecorder{w}, r)
	return w
}
func TestInspectionDefaultOffStrictAndBeforeDatabase(t *testing.T) {
	if h, err := NewGatewayInspectionHTTP(false, Engine{}, nil); h != nil || !errors.Is(err, ErrDisabled) {
		t.Fatal(h, err)
	}
	for _, x := range []Engine{{}, {Enabled: true}} {
		if h, err := NewGatewayInspectionHTTP(true, x, quarantineSecret); h != nil || err == nil {
			t.Fatal(h, err)
		}
	}
	if h, err := NewGatewayInspectionHTTP(true, Engine{Enabled: true, Pool: &pgxpool.Pool{}}, nil); h != nil || err == nil {
		t.Fatal(h, err)
	}
	h, err := NewGatewayInspectionHTTP(true, Engine{Enabled: true, Pool: &pgxpool.Pool{}}, quarantineSecret)
	if err != nil {
		t.Fatal(err)
	}
	token := quarantineToken(t, "a", "u", "session", "admin")
	good := `{"gateway_id":"g","event_id":"e","observed_at":"2026-11-21T00:00:00Z"}`
	for _, body := range []string{`{}`, `[]`, `null`, good + `{}`, strings.Replace(good, `"g"`, `null`, 1), strings.Replace(good, `"gateway_id"`, `"GATEWAY_ID"`, 1), strings.Replace(good, `"event_id":"e"`, `"event_id":"e","event_id":"e"`, 1), strings.Replace(good, `"g"`, `"g","tenant_id":"b"`, 1), strings.Replace(good, `00Z`, `00.000000001Z`, 1), strings.Replace(good, `"e"`, `"`+strings.Repeat("x", 257)+`"`, 1)} {
		if w := inspectionCall(h, token, body); w.Code != 400 {
			t.Fatal(body, w.Code)
		}
	}
	for _, mode := range []string{"expired", "no-exp", "future-iat", "http2", "fragment", "get", "query", "raw-path", "force-query", "plaintext", "forged-context", "AI", "missing", "duplicate-auth", "wrong-key", "viewer", "no-session", "wrong-media", "encoding", "large", "unsupported-writer"} {
		t.Run(mode, func(t *testing.T) {
			r := httptest.NewRequest("POST", inspectionPath, strings.NewReader(good))
			r.TLS = &tls.ConnectionState{HandshakeComplete: true}
			r.Header.Set("Authorization", "Bearer "+token)
			r.Header.Set("Content-Type", "application/json")
			switch mode {
			case "expired", "no-exp", "future-iat":
				c := auth.Claims{TenantID: "a", Role: "admin", RegisteredClaims: jwt.RegisteredClaims{Subject: "u", ID: "session", IssuedAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)), ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute))}}
				if mode == "no-exp" {
					c.ExpiresAt = nil
				}
				if mode == "future-iat" {
					c.IssuedAt = jwt.NewNumericDate(time.Now().Add(time.Hour))
					c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(2 * time.Hour))
				}
				raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(quarantineSecret)
				if err != nil {
					t.Fatal(err)
				}
				r.Header.Set("Authorization", "Bearer "+raw)
			case "http2":
				r.ProtoMajor = 2
			case "fragment":
				r.URL.Fragment = "x"
			case "get":
				r.Method = "GET"
			case "query":
				r.URL.RawQuery = "x=1"
			case "raw-path":
				r.URL.RawPath = inspectionPath
			case "force-query":
				r.URL.ForceQuery = true
			case "plaintext":
				r.TLS = nil
				r.Header.Set("X-Forwarded-Proto", "https")
			case "forged-context":
				r = r.WithContext(context.WithValue(r.Context(), auth.CtxRole, "admin"))
				r.Header.Set("Authorization", "Bearer forged")
			case "AI":
				r = r.WithContext(auth.AsAssistant(r.Context(), "a", "u", "admin"))
			case "missing":
				r.Header.Del("Authorization")
			case "duplicate-auth":
				r.Header.Add("Authorization", "Bearer "+token)
			case "wrong-key":
				r.Header.Set("Authorization", "Bearer "+token+"bad")
			case "viewer":
				r.Header.Set("Authorization", "Bearer "+quarantineToken(t, "a", "u", "session", "viewer"))
			case "no-session":
				r.Header.Set("Authorization", "Bearer "+quarantineToken(t, "a", "u", "", "admin"))
			case "wrong-media":
				r.Header.Set("Content-Type", "text/plain")
			case "encoding":
				r.Header.Set("Content-Encoding", "gzip")
			case "large":
				r.ContentLength = 2049
			}
			w := httptest.NewRecorder()
			if mode == "unsupported-writer" {
				h.ServeHTTP(w, r)
			} else {
				h.ServeHTTP(quarantineDeadlineRecorder{w}, r)
			}
			if w.Code < 400 || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
func TestInspectionActualComposedRuntimeStatesRedactionAndNoWrites(t *testing.T) {
	t.Parallel()
	x, e := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	seedQuarantineSession(t, x)
	c := runtimeExecutionConfig(t, x, f)
	c.Management.ReadOnlyInspection = true
	s, err := NewStagedGatewayRuntime(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	admission, management, cancel, done := startGatewayRuntime(t, s)
	defer cancel()
	if code, _ := durableRequest(t, f, admission, e); code != 200 {
		t.Fatal(code)
	}
	client := quarantineServerClient(t, f)
	token := quarantineToken(t, "a", "u", "session", "admin")
	inspect := func(want, quarantine string, started bool) {
		t.Helper()
		r, _ := http.NewRequest("POST", management+inspectionPath, strings.NewReader(inspectionBody(t, e)))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != 200 {
			t.Fatal(resp.StatusCode, string(b), err)
		}
		var out InspectionSummary
		if err = json.Unmarshal(b, &out); err != nil || out.State != want || out.Quarantine != quarantine || out.AttemptStarted != started || out.PlannedOccurrences != 2 || out.DeliveryConfirmed || out.RetryPermitted {
			t.Fatal(string(b), err)
		}
		var fields map[string]any
		if json.Unmarshal(b, &fields) != nil || len(fields) > 7 {
			t.Fatal("unexpected disclosure", string(b))
		}
		for _, secret := range []string{"synthetic@example.invalid", "again", "{value}", "tenant_id", "attempt_id", "plan_sha256", "envelope", "definition"} {
			if strings.Contains(string(b), secret) {
				t.Fatal("private retained details disclosed", secret)
			}
		}
	}
	inspect("pending", "", false)
	calls := 0
	if err = s.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, syntheticSender(func(context.Context, Envelope, Intent) error {
		calls++
		inspect("executing", "attempt_started_no_takeover_or_retry", true)
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	inspect("completed", "", true)
	// Revoked transport authority must not erase the operator's ability to inspect
	// retained uncertainty/history through their separately authenticated session.
	exec(t, x.Pool, "UPDATE gateways SET status='revoked' WHERE id='g'")
	inspect("completed", "", true)
	if calls != 2 {
		t.Fatal(calls)
	}
	historyCount(t, x, 0, "processing_started")
	historyCount(t, x, 1, "processing_completed")
	quarantineState(t, x, "revoked", 0)
	var audits int
	if err = x.Pool.QueryRow(t.Context(), "SELECT count(*) FROM audit_log").Scan(&audits); err != nil || audits != 0 {
		t.Fatal("inspection wrote audit/state", audits, err)
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if err = s.WaitDrained(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Log("ACTUAL_COMPOSED_RUNTIME_ADMIN_INSPECTION_PENDING_EXECUTING_COMPLETED_REVOKED_REDACTED_NO_WRITES_OK")
}
func TestInspectionLiveSessionTenantIntegrityAndUnknown(t *testing.T) {
	t.Parallel()
	x, e := denialFixture(t)
	seedQuarantineSession(t, x)
	mustAdmit(t, x, e)
	secret := append([]byte(nil), quarantineSecret...)
	h, err := NewGatewayInspectionHTTP(true, x, secret)
	if err != nil {
		t.Fatal(err)
	}
	for i := range secret {
		secret[i] = 0
	}
	token := quarantineToken(t, "a", "u", "session", "admin")
	body := inspectionBody(t, e)
	for _, pair := range [][2]string{
		{"UPDATE users SET role='viewer' WHERE id='u'", "UPDATE users SET role='admin' WHERE id='u'"},
		{"UPDATE users SET disabled_at=now() WHERE id='u'", "UPDATE users SET disabled_at=NULL WHERE id='u'"},
		{"UPDATE users SET must_change_credentials=true WHERE id='u'", "UPDATE users SET must_change_credentials=false WHERE id='u'"},
		{"UPDATE users SET tokens_valid_after=now() WHERE id='u'", "UPDATE users SET tokens_valid_after=NULL WHERE id='u'"},
		{"UPDATE user_sessions SET revoked_at=now() WHERE id='session'", "UPDATE user_sessions SET revoked_at=NULL WHERE id='session'"},
		{"UPDATE user_sessions SET tenant_id='b' WHERE id='session'", "UPDATE user_sessions SET tenant_id='a' WHERE id='session'"},
	} {
		exec(t, x.Pool, pair[0])
		if w := inspectionCall(h, token, body); w.Code != 403 {
			t.Fatal(pair[0], w.Code, w.Body.String())
		}
		exec(t, x.Pool, pair[1])
	}
	for _, change := range []func(*Envelope){func(e *Envelope) { e.GatewayID = "other" }, func(e *Envelope) { e.EventID = "missing" }, func(e *Envelope) { e.ObservedAt = e.ObservedAt.Add(time.Microsecond) }} {
		other := e
		change(&other)
		if w := inspectionCall(h, token, inspectionBody(t, other)); w.Code != 404 {
			t.Fatal(w.Code)
		}
	}
	if w := inspectionCall(h, quarantineToken(t, "b", "u", "session", "admin"), body); w.Code != 403 {
		t.Fatal(w.Code)
	}
	calls := 0
	if err = x.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, syntheticSender(func(context.Context, Envelope, Intent) error { calls++; return errors.New("synthetic uncertainty") })); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	w := inspectionCall(h, token, body)
	var out InspectionSummary
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.State != "outcome_unknown" || out.Quarantine != "attempt_outcome_unknown_no_retry" || !out.AttemptStarted || out.DeliveryConfirmed || out.RetryPermitted {
		t.Fatal(w.Code, w.Body.String())
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	exec(t, x.Pool, "UPDATE ingest_processing_v1 SET plan_sha256=decode(repeat('00',32),'hex')")
	if w := inspectionCall(h, token, body); w.Code != 404 {
		t.Fatal("corrupt evidence disclosed", w.Code)
	}
	t.Log("INSPECTION_CURRENT_ADMIN_TRACKED_SESSION_TENANT_EXACT_INPUT_CORRUPTION_AND_UNKNOWN_NO_RETRY_OK")
}
func TestInspectionTransportOptInAndScopeFence(t *testing.T) {
	t.Parallel()
	x, e := denialFixture(t)
	seedQuarantineSession(t, x)
	mustAdmit(t, x, e)
	f := httpTLSFixture(t)
	c := quarantineServerConfig(f, x)
	s, err := NewGatewayQuarantineServer(t.Context(), c, "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	if w := inspectionCall(s.transport.handler, quarantineToken(t, "a", "u", "session", "admin"), inspectionBody(t, e)); w.Code != 404 {
		t.Fatal("inspection on by default", w.Code)
	}
	h, err := NewGatewayInspectionHTTP(true, x, quarantineSecret)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := x.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(t.Context(), "LOCK TABLE user_customer_scope IN ROW EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	if w := inspectionCall(h, quarantineToken(t, "a", "u", "session", "admin"), inspectionBody(t, e)); w.Code != 503 {
		t.Fatal("busy scope permitted", w.Code)
	}
	if err = tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if w := inspectionCall(h, quarantineToken(t, "a", "u", "session", "admin"), inspectionBody(t, e)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	counts(t, x.Pool, e.EventID, 1)
	historyCount(t, x, 0, "processing_started")
}

func TestInspectionActualHTTPSCapacityDemotionAndRecovery(t *testing.T) {
	t.Parallel()
	x, e := denialFixture(t)
	mustAdmit(t, x, e)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	seedQuarantineSession(t, x)
	cfg := quarantineServerConfig(f, x)
	cfg.ReadOnlyInspection = true
	management, err := NewGatewayQuarantineServer(t.Context(), cfg, "../../migrations")
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
	endpoint := startAdmissionServer(t, management.transport)
	client := quarantineServerClient(t, f)
	client.Timeout = 8 * time.Second
	token, body := quarantineToken(t, "a", "u", "session", "admin"), inspectionBody(t, e)
	call := func() (int, error) {
		r, e := http.NewRequestWithContext(t.Context(), "POST", endpoint+inspectionPath, strings.NewReader(body))
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
	quarantineState(t, x, "active", 0)
	historyCount(t, x, 0, "processing_started")
	t.Log("INSPECTION_ACTUAL_HTTPS_FOUR_SLOT_FIFTH_REFUSAL_DEMOTION_NO_DISCLOSURE_RECOVERY_OK")
}

func TestInspectionExpiryCancelsRealDBWaitAndScopeRefuses(t *testing.T) {
	t.Parallel()
	x, e := denialFixture(t)
	seedQuarantineSession(t, x)
	mustAdmit(t, x, e)
	h, err := NewGatewayInspectionHTTP(true, x, quarantineSecret)
	if err != nil {
		t.Fatal(err)
	}
	exec(t, x.Pool, "INSERT INTO customers(id,tenant_id,name) VALUES('customer','a','Synthetic');INSERT INTO user_customer_scope(tenant_id,user_id,customer_id) VALUES('a','u','customer')")
	if w := inspectionCall(h, quarantineToken(t, "a", "u", "session", "admin"), inspectionBody(t, e)); w.Code != 403 {
		t.Fatal("scoped admin allowed", w.Code)
	}
	exec(t, x.Pool, "DELETE FROM user_customer_scope")
	block, err := x.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(block)
	if _, err = block.Exec(t.Context(), "UPDATE user_sessions SET revoked_at=NULL WHERE id='session'"); err != nil {
		t.Fatal(err)
	}
	claims := auth.Claims{TenantID: "a", Role: "admin", RegisteredClaims: jwt.RegisteredClaims{Subject: "u", ID: "session", IssuedAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)), ExpiresAt: jwt.NewNumericDate(time.Now().Add(2 * time.Second))}}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(quarantineSecret)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	body := inspectionBody(t, e)
	go func() { done <- inspectionCall(h, token, body) }()
	waitQuarantineSQL(t, x, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'SELECT created_at,revoked_at%'")
	select {
	case w := <-done:
		if w.Code < 400 {
			t.Fatal("expired authority disclosed state", w.Code, w.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("expired read not cancelled")
	}
	if err = block.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	counts(t, x.Pool, e.EventID, 1)
	historyCount(t, x, 0, "processing_started")
	t.Log("INSPECTION_SCOPED_ADMIN_REFUSED_SESSION_EXPIRY_CANCELS_ACTUAL_DB_WAITER_NO_DISCLOSURE_OK")
}

func TestInspectionActualTLSSlowBodyBoundedBeforeDB(t *testing.T) {
	t.Parallel()
	h, err := NewGatewayInspectionHTTP(true, Engine{Enabled: true, Pool: &pgxpool.Pool{}}, quarantineSecret)
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
	_, err = fmt.Fprintf(conn, "POST /staged/processing-v1/inspect-input HTTP/1.1\r\nHost: localhost\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{", quarantineToken(t, "a", "u", "session", "admin"))
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
	t.Log("INSPECTION_ACTUAL_TLS_SLOW_BODY_DEADLINE_NO_DB_OK")
}
