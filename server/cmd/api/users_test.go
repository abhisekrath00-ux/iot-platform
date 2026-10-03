package main

import (
	"context"
	"encoding/json"
	"github.com/golang-jwt/jwt/v5"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

func TestIntegrationUserManagementAndLocalLogin(t *testing.T) {
	t.Setenv("LOCAL_LOGIN", "1")
	s, _ := testServer(t)
	s.secret = []byte("test-secret-test-secret-test-secret")
	seed(t, s, "itest-um1")
	seed(t, s, "itest-um2")
	ctx := t.Context()
	pool := s.st.Pool
	clean := func() {
		pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id IN ('itest-um1','itest-um2')`)
		pool.Exec(ctx, `DELETE FROM users WHERE email ILIKE '%@um-test.example'`)
		pool.Exec(ctx, `DELETE FROM users WHERE id IN ('um-admin','um-admin2') `)
	}
	clean()
	t.Cleanup(clean)
	pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('um-admin','itest-um1','um-admin@um-test.example','Admin','admin')`)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/users", s.listUsers)
	mux.HandleFunc("POST /v1/users", s.createUser)
	mux.HandleFunc("PUT /v1/users/{id}", s.updateUser)
	mux.HandleFunc("PUT /v1/users/{id}/password", s.resetUserPassword)
	mux.HandleFunc("POST /v1/me/password", s.changeOwnPassword)
	mux.HandleFunc("GET /v1/devices", s.listDevices)
	mux.HandleFunc("POST /auth/login", s.localLogin)
	h := s.activeUser(mux)
	login := func(email, pw string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/auth/login", strings.NewReader(`{"email":"`+email+`","password":"`+pw+`"}`))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	adm := func(method, path, body string) *httptest.ResponseRecorder {
		return callAs(h, "itest-um1", "um-admin", "admin", method, path, body)
	}
	const pw = "a-long-enough-password"
	if w := adm("POST", "/v1/users", `{"email":"bob@um-test.example","display_name":"Bob","role":"operator","password":"short"}`); w.Code != 400 {
		t.Fatalf("weak password = %d", w.Code)
	}
	if w := adm("POST", "/v1/users", `{"email":"bob@um-test.example","display_name":"Bob","role":"root","password":"`+pw+`"}`); w.Code != 400 {
		t.Fatalf("bad role = %d", w.Code)
	}
	if w := adm("POST", "/v1/users", `{"email":"<x>","display_name":"Bob","role":"viewer"}`); w.Code != 400 {
		t.Fatalf("bad email = %d", w.Code)
	}
	w := adm("POST", "/v1/users", `{"email":"bob@um-test.example","display_name":"Bob","role":"operator","password":"`+pw+`"}`)
	if w.Code != 201 {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	var bob struct{ ID string }
	json.Unmarshal(w.Body.Bytes(), &bob)
	if adm("POST", "/v1/users", `{"email":"BOB@um-test.example","display_name":"Dup","role":"viewer"}`).Code != 409 {
		t.Fatal("duplicate email accepted")
	}
	// non-admins and API keys cannot manage users
	if callAs(h, "itest-um1", bob.ID, "operator", "GET", "/v1/users", "").Code != 403 {
		t.Fatal("operator listed users")
	}
	rk := httptest.NewRequest("GET", "/v1/users", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, rk.WithContext(withKeyCtx(rk, "itest-um1", "um-admin", "admin")))
	if rec.Code != 403 {
		t.Fatalf("api key listed users = %d", rec.Code)
	}
	// another tenant's admin cannot touch Bob
	if callAs(h, "itest-um2", "x", "admin", "PUT", "/v1/users/"+bob.ID, `{"role":"admin"}`).Code != 404 {
		t.Fatal("cross-tenant update")
	}
	if callAs(h, "itest-um2", "x", "admin", "PUT", "/v1/users/"+bob.ID+"/password", `{"password":"`+pw+`-x"}`).Code != 404 {
		t.Fatal("cross-tenant password reset")
	}
	// login
	var tok struct{ Token string }
	lw := login("bob@um-test.example", pw)
	if lw.Code != 200 {
		t.Fatalf("login = %d %s", lw.Code, lw.Body)
	}
	json.Unmarshal(lw.Body.Bytes(), &tok)
	claims := &auth.Claims{}
	if _, err := jwtParse(tok.Token, s.secret, claims); err != nil || claims.TenantID != "itest-um1" || claims.Role != "operator" || claims.Subject != bob.ID {
		t.Fatalf("token claims %+v %v", claims, err)
	}
	if login("bob@um-test.example", "wrong-password-here").Code != 401 || login("nobody@um-test.example", pw).Code != 401 {
		t.Fatal("bad credentials accepted")
	}
	// lockout after repeated failures, even with the right password afterwards
	for i := 0; i < 5; i++ {
		login("bob@um-test.example", "wrong-password-here")
	}
	if c := login("bob@um-test.example", pw).Code; c != 429 {
		t.Fatalf("locked account login = %d, want 429", c)
	}
	// admin reset clears the lock
	if adm("PUT", "/v1/users/"+bob.ID+"/password", `{"password":"another-long-password"}`).Code != 204 {
		t.Fatal("reset")
	}
	if login("bob@um-test.example", "another-long-password").Code != 200 || login("bob@um-test.example", pw).Code != 401 {
		t.Fatal("reset did not take")
	}
	// own password change
	if callAs(h, "itest-um1", bob.ID, "operator", "POST", "/v1/me/password", `{"current":"nope-nope-nope-nope","new":"third-long-password"}`).Code != 403 {
		t.Fatal("changed password with a wrong current one")
	}
	if callAs(h, "itest-um1", bob.ID, "operator", "POST", "/v1/me/password", `{"current":"another-long-password","new":"third-long-password"}`).Code != 204 {
		t.Fatal("own password change")
	}
	// the last active admin cannot be demoted or disabled, nor yourself
	if c := adm("PUT", "/v1/users/um-admin", `{"role":"viewer"}`).Code; c != 409 {
		t.Fatalf("self demote = %d", c)
	}
	if w := adm("PUT", "/v1/users/"+bob.ID, `{"role":"admin"}`); w.Code != 204 {
		t.Fatalf("promote = %d %s", w.Code, w.Body)
	}
	if c := callAs(h, "itest-um1", bob.ID, "admin", "PUT", "/v1/users/um-admin", `{"disabled":true}`).Code; c != 204 {
		t.Fatalf("admin disabling the other admin = %d", c)
	}
	if c := callAs(h, "itest-um1", bob.ID, "admin", "PUT", "/v1/users/"+bob.ID, `{"disabled":true}`).Code; c != 409 {
		t.Fatalf("disable self = %d", c)
	}
	// a disabled user's existing token stops working at once, and they cannot log in
	if c := callAs(h, "itest-um1", "um-admin", "admin", "GET", "/v1/devices", "").Code; c != 401 {
		t.Fatalf("disabled admin's token still works = %d", c)
	}
	if adm2 := callAs(h, "itest-um1", bob.ID, "admin", "PUT", "/v1/users/um-admin", `{"disabled":false}`); adm2.Code != 204 {
		t.Fatal("re-enable")
	}
	if c := callAs(h, "itest-um1", "um-admin", "admin", "GET", "/v1/devices", "").Code; c != 200 {
		t.Fatalf("re-enabled user = %d", c)
	}
	// local login switched off
	t.Setenv("LOCAL_LOGIN", "")
	if login("bob@um-test.example", "third-long-password").Code != 404 {
		t.Fatal("login worked with LOCAL_LOGIN off")
	}
}

func withKeyCtx(r *http.Request, tenant, user, role string) context.Context {
	ctx := context.WithValue(r.Context(), auth.CtxTenant, tenant)
	ctx = context.WithValue(ctx, auth.CtxUser, user)
	ctx = context.WithValue(ctx, auth.CtxRole, role)
	return context.WithValue(ctx, auth.CtxViaKey, true)
}

func jwtParse(tok string, secret []byte, c *auth.Claims) (*jwt.Token, error) {
	return jwt.ParseWithClaims(tok, c, func(*jwt.Token) (any, error) { return secret, nil })
}
