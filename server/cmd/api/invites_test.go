package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIntegrationInvitations(t *testing.T) {
	t.Setenv("LOCAL_LOGIN", "1")
	s, _ := testServer(t)
	seed(t, s, "itest-iv1")
	seed(t, s, "itest-iv2")
	ctx := context.Background()
	pool := s.st.Pool
	clean := func() {
		pool.Exec(ctx, `DELETE FROM user_invites WHERE tenant_id IN ('itest-iv1','itest-iv2')`)
		pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id IN ('itest-iv1','itest-iv2')`)
		pool.Exec(ctx, `DELETE FROM users WHERE email ILIKE '%@iv-test.example' OR id IN ('iv-admin','iv-op')`)
	}
	clean()
	t.Cleanup(clean)
	pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('iv-admin','itest-iv1','iv-admin@iv-test.example','A','admin')`)
	pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('iv-op','itest-iv1','iv-op@iv-test.example','O','operator')`)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/users/invites", s.listInvites)
	mux.HandleFunc("POST /v1/users/invites", s.createInvite)
	mux.HandleFunc("DELETE /v1/users/invites/{id}", s.revokeInvite)
	mux.HandleFunc("POST /auth/accept-invite", s.acceptInvite)
	h := s.activeUser(mux)
	adm := func(method, path, body string) *httptest.ResponseRecorder {
		return callAs(h, "itest-iv1", "iv-admin", "admin", method, path, body)
	}
	accept := func(tok, name, pw string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]string{"token": tok, "display_name": name, "password": pw})
		r := httptest.NewRequest("POST", "/auth/accept-invite", strings.NewReader(string(b)))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if callAs(h, "itest-iv1", "iv-op", "operator", "POST", "/v1/users/invites", `{"email":"x@iv-test.example","role":"viewer"}`).Code != 403 {
		t.Fatal("non-admin created an invitation")
	}
	if adm("POST", "/v1/users/invites", `{"email":"bad","role":"viewer"}`).Code != 400 || adm("POST", "/v1/users/invites", `{"email":"n@iv-test.example","role":"root"}`).Code != 400 {
		t.Fatal("validation")
	}
	w := adm("POST", "/v1/users/invites", `{"email":"new@iv-test.example","role":"operator"}`)
	if w.Code != 201 {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	var inv struct{ ID, Token string }
	json.Unmarshal(w.Body.Bytes(), &inv)
	var stored int
	pool.QueryRow(ctx, `SELECT count(*) FROM user_invites WHERE tenant_id='itest-iv1' AND convert_from(token_hash,'SQL_ASCII') = $1`, inv.Token).Scan(&stored)
	if stored != 0 {
		t.Fatal("token stored in the clear")
	}
	if l := adm("GET", "/v1/users/invites", ""); !strings.Contains(l.Body.String(), "new@iv-test.example") || strings.Contains(l.Body.String(), inv.Token) {
		t.Fatalf("list must show the invite but never the token: %s", l.Body)
	}
	if callAs(h, "itest-iv2", "x", "admin", "GET", "/v1/users/invites", "").Body.String() != "[]\n" {
		t.Fatal("another tenant sees the invitation")
	}
	if accept("wrong-wrong-wrong-wrong-wrong", "New", "a-long-enough-password").Code != 400 {
		t.Fatal("bad token accepted")
	}
	if accept(inv.Token, "New", "short").Code != 400 {
		t.Fatal("weak password accepted")
	}
	a := accept(inv.Token, "New User", "a-long-enough-password")
	if a.Code != 200 || !strings.Contains(a.Body.String(), `"role":"operator"`) || !strings.Contains(a.Body.String(), `"tenant_id":"itest-iv1"`) {
		t.Fatalf("accept = %d %s", a.Code, a.Body)
	}
	if accept(inv.Token, "Again", "a-long-enough-password").Code != 400 {
		t.Fatal("invitation reused")
	}
	if adm("POST", "/v1/users/invites", `{"email":"NEW@iv-test.example","role":"viewer"}`).Code != 409 {
		t.Fatal("invited an existing user")
	}
	// a scoped invitation creates a customer-scoped user; a foreign customer is refused
	pool.Exec(ctx, `DELETE FROM customers WHERE tenant_id IN ('itest-iv1','itest-iv2')`)
	pool.Exec(ctx, `INSERT INTO customers(id,tenant_id,name) VALUES('iv-c1','itest-iv1','C1'),('iv-c2','itest-iv2','C2')`)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM user_customer_scope WHERE tenant_id='itest-iv1'`)
		pool.Exec(ctx, `DELETE FROM customers WHERE id IN ('iv-c1','iv-c2')`)
	})
	if adm("POST", "/v1/users/invites", `{"email":"sc@iv-test.example","role":"viewer","customer_id":"iv-c2"}`).Code != 404 {
		t.Fatal("invited into another tenant's customer")
	}
	w = adm("POST", "/v1/users/invites", `{"email":"sc@iv-test.example","role":"viewer","customer_id":"iv-c1"}`)
	json.Unmarshal(w.Body.Bytes(), &inv)
	if accept(inv.Token, "Scoped", "a-long-enough-password").Code != 200 {
		t.Fatal("scoped accept")
	}
	var sc int
	pool.QueryRow(ctx, `SELECT count(*) FROM user_customer_scope s JOIN users u ON u.id=s.user_id WHERE u.email='sc@iv-test.example' AND s.customer_id='iv-c1'`).Scan(&sc)
	if sc != 1 {
		t.Fatal("scope not applied")
	}
	// expiry and revoke
	w = adm("POST", "/v1/users/invites", `{"email":"late@iv-test.example","role":"viewer"}`)
	json.Unmarshal(w.Body.Bytes(), &inv)
	pool.Exec(ctx, `UPDATE user_invites SET expires_at=now()-interval '1 minute' WHERE id=$1`, inv.ID)
	if accept(inv.Token, "Late", "a-long-enough-password").Code != 400 {
		t.Fatal("expired invitation accepted")
	}
	w = adm("POST", "/v1/users/invites", `{"email":"gone@iv-test.example","role":"viewer"}`)
	json.Unmarshal(w.Body.Bytes(), &inv)
	if callAs(h, "itest-iv2", "x", "admin", "DELETE", "/v1/users/invites/"+inv.ID, "").Code != 404 {
		t.Fatal("another tenant revoked it")
	}
	if adm("DELETE", "/v1/users/invites/"+inv.ID, "").Code != 204 || accept(inv.Token, "Gone", "a-long-enough-password").Code != 400 {
		t.Fatal("revoke")
	}
	t.Setenv("LOCAL_LOGIN", "")
	if adm("POST", "/v1/users/invites", `{"email":"z@iv-test.example","role":"viewer"}`).Code != 409 || accept(inv.Token, "Z", "a-long-enough-password").Code != 404 {
		t.Fatal("must be off without LOCAL_LOGIN")
	}
}
