package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

// TestIntegrationSessionRevocationAndLiveRole proves that a demoted user's already-issued token
// loses its old role at once, and that a password reset invalidates sessions issued before it.
func TestIntegrationSessionRevocationAndLiveRole(t *testing.T) {
	s, _ := testServer(t)
	ctx := context.Background()
	pool := s.st.Pool
	seed(t, s, "itest-sr1")
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM users WHERE tenant_id='itest-sr1'`)
		pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id='itest-sr1'`)
	})
	pool.Exec(ctx, `DELETE FROM users WHERE tenant_id='itest-sr1'`)
	pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('sr-u','itest-sr1','sr-u@sr-test.example','U','operator')`)
	secret := []byte("sr-secret")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/probe", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(auth.Role(r)))
	})
	mux.HandleFunc("GET /v1/me", s.me)
	mux.HandleFunc("POST /v1/me/sessions/revoke", s.revokeOwnSessions)
	h := authMiddleware(secret)(s.activeUser(mux))
	token := func(role string, iat time.Time) string {
		tok := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{TenantID: "itest-sr1", Role: role,
			RegisteredClaims: jwt.RegisteredClaims{Subject: "sr-u", IssuedAt: jwt.NewNumericDate(iat), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}})
		x, _ := tok.SignedString(secret)
		return x
	}
	get := func(tk string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/probe", nil)
		r.Header.Set("Authorization", "Bearer "+tk)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	old := token("admin", time.Now().Add(-time.Minute)) // claims admin but the user is an operator
	if w := get(old); w.Code != 200 || w.Body.String() != "operator" {
		t.Fatalf("token role must be overridden by the database role: %d %q", w.Code, w.Body.String())
	}
	// revoke: password reset sets tokens_valid_after; older tokens die, newer ones live
	pool.Exec(ctx, `UPDATE users SET tokens_valid_after=now() WHERE id='sr-u'`)
	if w := get(old); w.Code != 401 {
		t.Fatalf("token issued before revocation = %d", w.Code)
	}
	if w := get(token("operator", time.Now().Add(2*time.Second))); w.Code != 200 {
		t.Fatalf("token issued after revocation = %d", w.Code)
	}
	// /v1/me reports the live role and the customer scope
	pool.Exec(ctx, `INSERT INTO customers(id,tenant_id,name) VALUES('sr-c','itest-sr1','Plant A') ON CONFLICT DO NOTHING`)
	pool.Exec(ctx, `INSERT INTO user_customer_scope(tenant_id,user_id,customer_id) VALUES('itest-sr1','sr-u','sr-c') ON CONFLICT DO NOTHING`)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM user_customer_scope WHERE tenant_id='itest-sr1'`)
		pool.Exec(ctx, `DELETE FROM customers WHERE id='sr-c'`)
	})
	r := httptest.NewRequest("GET", "/v1/me", nil)
	r.Header.Set("Authorization", "Bearer "+token("admin", time.Now().Add(3*time.Second)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"role":"operator"`) || !strings.Contains(w.Body.String(), `"customer_name":"Plant A"`) {
		t.Fatalf("me = %d %s", w.Code, w.Body)
	}
	// sign out everywhere: the token used for the call and every earlier one stop working
	pool.Exec(ctx, `UPDATE users SET tokens_valid_after=NULL WHERE id='sr-u'`)
	cur := token("operator", time.Now().Add(-2*time.Second))
	rv := httptest.NewRequest("POST", "/v1/me/sessions/revoke", nil)
	rv.Header.Set("Authorization", "Bearer "+cur)
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, rv)
	if rw.Code != 204 || get(cur).Code != 401 {
		t.Fatalf("revoke own sessions = %d", rw.Code)
	}
}

// callAsSession is callAs plus a tracked-session id on the request context.
func callAsSession(h http.Handler, tenant, user, role, session, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	ctx := context.WithValue(r.Context(), auth.CtxTenant, tenant)
	ctx = context.WithValue(ctx, auth.CtxUser, user)
	ctx = context.WithValue(ctx, auth.CtxRole, role)
	if session != "" {
		ctx = context.WithValue(ctx, auth.CtxSession, session)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r.WithContext(ctx))
	return w
}

// Tracked sessions: a login stores a row, the token carries it as jti, one
// session can be revoked without touching the others, and a revoked or
// unknown session id fails closed at the per-request check.
func TestIntegrationTrackedSessions(t *testing.T) {
	t.Setenv("LOCAL_LOGIN", "1")
	s, _ := testServer(t)
	s.secret = []byte("test-secret-test-secret-test-secret")
	seed(t, s, "itest-ss1")
	seed(t, s, "itest-ss2")
	ctx := t.Context()
	pool := s.st.Pool
	clean := func() {
		pool.Exec(ctx, `DELETE FROM user_sessions WHERE tenant_id IN ('itest-ss1','itest-ss2')`)
		pool.Exec(ctx, `DELETE FROM users WHERE id IN ('ss-u1','ss-u2')`)
		pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id='itest-ss1'`)
	}
	clean()
	t.Cleanup(clean)
	hash, _ := auth.HashPassword("a-long-test-passphrase-1")
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role,password_hash) VALUES
	  ('ss-u1','itest-ss1','ss1@x.local','S','admin',$1),('ss-u2','itest-ss1','ss2@x.local','T','viewer',$1)`, hash); err != nil {
		t.Fatal(err)
	}

	// Two real logins produce two tracked sessions whose tokens carry them as jti.
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/login", s.localLogin)
	login := func() (token, jti string) {
		r := httptest.NewRequest("POST", "/auth/login", strings.NewReader(`{"email":"ss1@x.local","password":"a-long-test-passphrase-1"}`))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("login = %d %s", w.Code, w.Body.String())
		}
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		token, _ = out["token"].(string)
		var c auth.Claims
		if _, err := jwt.ParseWithClaims(token, &c, func(tk *jwt.Token) (any, error) { return s.secret, nil }, jwt.WithValidMethods([]string{"HS256"})); err != nil {
			t.Fatalf("token parses: %v", err)
		}
		return token, c.ID
	}
	_, jti1 := login()
	_, jti2 := login()
	if jti1 == "" || jti2 == "" || jti1 == jti2 {
		t.Fatalf("each login needs its own jti: %q %q", jti1, jti2)
	}
	var rows int
	pool.QueryRow(ctx, `SELECT count(*) FROM user_sessions WHERE tenant_id='itest-ss1' AND user_id='ss-u1' AND revoked_at IS NULL`).Scan(&rows)
	if rows != 2 {
		t.Fatalf("tracked sessions = %d, want 2", rows)
	}

	api := http.NewServeMux()
	api.HandleFunc("GET /v1/me/sessions", s.listOwnSessions)
	api.HandleFunc("DELETE /v1/me/sessions/{id}", s.revokeOwnSession)
	api.HandleFunc("POST /v1/me/sessions/revoke", s.revokeOwnSessions)
	api.HandleFunc("GET /v1/me", s.me)
	h := s.activeUser(api)

	// The list flags the session carrying the request.
	w := callAsSession(h, "itest-ss1", "ss-u1", "admin", jti1, "GET", "/v1/me/sessions", "")
	var list []map[string]any
	json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != 200 || len(list) != 2 {
		t.Fatalf("list = %d %v", w.Code, list)
	}
	for _, it := range list {
		if it["current"] == true && it["id"] != jti1 {
			t.Fatalf("wrong current flag: %v", it)
		}
	}

	// Revoke the other session: its requests die, the current one keeps working.
	if w := callAsSession(h, "itest-ss1", "ss-u1", "admin", jti1, "DELETE", "/v1/me/sessions/"+jti2, ""); w.Code != 204 {
		t.Fatalf("revoke = %d", w.Code)
	}
	if w := callAsSession(h, "itest-ss1", "ss-u1", "admin", jti2, "GET", "/v1/me", ""); w.Code != 401 {
		t.Fatalf("revoked session must fail closed: %d", w.Code)
	}
	if w := callAsSession(h, "itest-ss1", "ss-u1", "admin", jti1, "GET", "/v1/me", ""); w.Code != 200 {
		t.Fatalf("live session = %d", w.Code)
	}

	// Unknown ids and another user's sessions are 404; a jti-less request (pre-feature token) still passes.
	if w := callAsSession(h, "itest-ss1", "ss-u1", "admin", jti1, "DELETE", "/v1/me/sessions/ss-nope", ""); w.Code != 404 {
		t.Fatalf("unknown = %d", w.Code)
	}
	pool.Exec(ctx, `INSERT INTO user_sessions(id,tenant_id,user_id) VALUES('ss-other','itest-ss1','ss-u2')`)
	if w := callAsSession(h, "itest-ss1", "ss-u1", "admin", jti1, "DELETE", "/v1/me/sessions/ss-other", ""); w.Code != 404 {
		t.Fatalf("other user's session = %d", w.Code)
	}
	if w := callAsSession(h, "itest-ss1", "ss-u1", "admin", "", "GET", "/v1/me", ""); w.Code != 200 {
		t.Fatalf("jti-less token must stay valid: %d", w.Code)
	}
	// An unknown jti fails closed even though the user is fine.
	if w := callAsSession(h, "itest-ss1", "ss-u1", "admin", "ss-ghost", "GET", "/v1/me", ""); w.Code != 401 {
		t.Fatalf("unknown session id = %d", w.Code)
	}

	// Sign out everywhere empties the list and kills the remaining session.
	if w := callAsSession(h, "itest-ss1", "ss-u1", "admin", jti1, "POST", "/v1/me/sessions/revoke", ""); w.Code != 204 {
		t.Fatalf("revoke all = %d", w.Code)
	}
	pool.QueryRow(ctx, `SELECT count(*) FROM user_sessions WHERE tenant_id='itest-ss1' AND user_id='ss-u1' AND revoked_at IS NULL`).Scan(&rows)
	if rows != 0 {
		t.Fatalf("sessions after revoke-all = %d, want 0", rows)
	}
	if w := callAsSession(h, "itest-ss1", "ss-u1", "admin", jti1, "GET", "/v1/me", ""); w.Code != 401 {
		t.Fatalf("session must be dead after revoke-all: %d", w.Code)
	}
}
