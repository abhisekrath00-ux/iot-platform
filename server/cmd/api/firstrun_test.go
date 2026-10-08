package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// First-run default login: the seeded password is stored hashed, the server blocks everything except the
// credentials change until it is replaced, the replacement is validated, and an update never re-forces anyone.
func TestIntegrationFirstRunCredentials(t *testing.T) {
	t.Setenv("LOCAL_LOGIN", "1")
	s, _ := testServer(t)
	s.secret = []byte("test-secret-test-secret-test-secret")
	seed(t, s, "itest-fr1")
	ctx := t.Context()
	pool := s.st.Pool
	clean := func() {
		pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id='itest-fr1'`)
		pool.Exec(ctx, `DELETE FROM users WHERE id IN ('fr-admin','fr-other')`)
	}
	clean()
	t.Cleanup(clean)
	h0, err := auth.HashPassword(auth.DefaultAdminPassword)
	if err != nil {
		t.Fatal(err)
	}
	pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role,password_hash,must_change_credentials) VALUES('fr-admin','itest-fr1',$1,'Administrator','admin',$2,true)`, "fr-default@fr-test.example", h0)
	pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role,password_hash) VALUES('fr-other','itest-fr1','taken@fr-test.example','Other','viewer',$1)`, h0)

	var stored string
	pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id='fr-admin'`).Scan(&stored)
	if !strings.HasPrefix(stored, "pbkdf2-sha256$") || strings.Contains(stored, auth.DefaultAdminPassword) {
		t.Fatalf("seeded password must be a salted hash, got %q", stored)
	}
	if !auth.VerifyPassword(auth.DefaultAdminPassword, stored) {
		t.Fatal("seeded hash does not verify the default password")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/devices", s.listDevices)
	mux.HandleFunc("GET /v1/me", s.me)
	mux.HandleFunc("POST /v1/me/password", s.changeOwnPassword)
	mux.HandleFunc("POST /v1/me/credentials", s.changeCredentials)
	mux.HandleFunc("POST /auth/login", s.localLogin)
	h := s.activeUser(mux)
	login := func(email, pw string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/auth/login", strings.NewReader(`{"email":"`+email+`","password":"`+pw+`"}`))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	as := func(method, path, body string) *httptest.ResponseRecorder {
		return callAs(h, "itest-fr1", "fr-admin", "admin", method, path, body)
	}

	w := login("fr-default@fr-test.example", auth.DefaultAdminPassword)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"must_change_credentials":true`) {
		t.Fatalf("default login = %d %s", w.Code, w.Body.String())
	}
	// Server-side block: every route except /v1/me and the credentials change.
	for _, c := range []struct{ m, p string }{{"GET", "/v1/devices"}, {"POST", "/v1/me/password"}} {
		if w := as(c.m, c.p, `{}`); w.Code != 403 || !strings.Contains(w.Body.String(), "credentials_change_required") {
			t.Fatalf("%s %s while default = %d %s", c.m, c.p, w.Code, w.Body.String())
		}
	}
	if w := as("GET", "/v1/me", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"must_change_credentials":true`) {
		t.Fatalf("me = %d %s", w.Code, w.Body.String())
	}
	const good = "a-new-long-passphrase-9"
	bad := []struct{ name, body string }{
		{"wrong current", `{"current":"nope-nope-nope","new_email":"me@fr-test.example","new":"` + good + `"}`},
		{"default email kept", `{"current":"` + auth.DefaultAdminPassword + `","new_email":"Admin@HexThings.com","new":"` + good + `"}`},
		{"default password reused", `{"current":"` + auth.DefaultAdminPassword + `","new_email":"me@fr-test.example","new":"` + auth.DefaultAdminPassword + `"}`},
		{"short password", `{"current":"` + auth.DefaultAdminPassword + `","new_email":"me@fr-test.example","new":"short"}`},
		{"password equals email", `{"current":"` + auth.DefaultAdminPassword + `","new_email":"me-me-me-me@fr-test.example","new":"me-me-me-me@fr-test.example"}`},
		{"invalid email", `{"current":"` + auth.DefaultAdminPassword + `","new_email":"<x>","new":"` + good + `"}`},
		{"email taken", `{"current":"` + auth.DefaultAdminPassword + `","new_email":"taken@fr-test.example","new":"` + good + `"}`},
	}
	for _, c := range bad {
		if w := as("POST", "/v1/me/credentials", c.body); w.Code < 400 {
			t.Fatalf("%s accepted: %d", c.name, w.Code)
		}
	}
	var still bool
	pool.QueryRow(ctx, `SELECT must_change_credentials FROM users WHERE id='fr-admin'`).Scan(&still)
	if !still {
		t.Fatal("a rejected change cleared the flag")
	}
	w = as("POST", "/v1/me/credentials", `{"current":"`+auth.DefaultAdminPassword+`","new_email":"owner@fr-test.example","new":"`+good+`"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"token"`) {
		t.Fatalf("change = %d %s", w.Code, w.Body.String())
	}
	var email, hash string
	var mc bool
	pool.QueryRow(ctx, `SELECT email, password_hash, must_change_credentials FROM users WHERE id='fr-admin'`).Scan(&email, &hash, &mc)
	if email != "owner@fr-test.example" || mc || strings.Contains(hash, good) || !auth.VerifyPassword(good, hash) || auth.VerifyPassword(auth.DefaultAdminPassword, hash) {
		t.Fatalf("after change: email=%s mustChange=%v hash=%q", email, mc, hash)
	}
	if w := as("GET", "/v1/devices", ""); w.Code != 200 {
		t.Fatalf("devices after change = %d", w.Code)
	}
	if login("fr-default@fr-test.example", auth.DefaultAdminPassword).Code != 401 || login("owner@fr-test.example", auth.DefaultAdminPassword).Code != 401 {
		t.Fatal("the default login still works after the change")
	}
	if login("owner@fr-test.example", good).Code != 200 {
		t.Fatal("new login does not work")
	}
	// An update re-runs every migration: the changed admin must stay changed, with the same hash.
	mig, err := os.ReadFile("../../migrations/0068_must_change_credentials.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(mig)); err != nil {
		t.Fatal(err)
	}
	var email2, hash2 string
	var mc2 bool
	pool.QueryRow(ctx, `SELECT email, password_hash, must_change_credentials FROM users WHERE id='fr-admin'`).Scan(&email2, &hash2, &mc2)
	if email2 != email || hash2 != hash || mc2 {
		t.Fatal("re-running the migration changed an already-changed admin")
	}
	// The other (flag false) user is never blocked or forced.
	if w := callAs(h, "itest-fr1", "fr-other", "viewer", "GET", "/v1/devices", ""); w.Code == 403 && strings.Contains(w.Body.String(), "credentials_change_required") {
		t.Fatal("an ordinary user was forced to change credentials")
	}
	// Lockout still protects the known default: repeated wrong passwords lock it even for the right one.
	pool.Exec(ctx, `UPDATE users SET email='fr-default@fr-test.example', password_hash=$1, must_change_credentials=true WHERE id='fr-admin'`, h0)
	for i := 0; i < maxFailedLogins; i++ {
		login("fr-default@fr-test.example", "wrong-wrong-wrong")
	}
	if w := login("fr-default@fr-test.example", auth.DefaultAdminPassword); w.Code != http.StatusTooManyRequests {
		t.Fatalf("locked default account login = %d", w.Code)
	}
}
