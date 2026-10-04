package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/secrets"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/totp"
)

func TestIntegrationLoginMFA(t *testing.T) {
	t.Setenv("LOCAL_LOGIN", "1")
	s, _ := testServer(t)
	s.secret = []byte("test-secret-test-secret-test-secret")
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 7)
	}
	s.secrets = &secrets.Store{Pool: s.st.Pool, Key: key}
	seed(t, s, "itest-mf1")
	seed(t, s, "itest-mf2")
	ctx := t.Context()
	pool := s.st.Pool
	clean := func() {
		pool.Exec(ctx, `DELETE FROM user_totp WHERE tenant_id IN ('itest-mf1','itest-mf2')`)
		pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id IN ('itest-mf1','itest-mf2')`)
		pool.Exec(ctx, `DELETE FROM users WHERE tenant_id IN ('itest-mf1','itest-mf2')`)
	}
	clean()
	t.Cleanup(clean)
	const pw = "a-long-enough-password"
	hash, _ := auth.HashPassword(pw)
	for _, u := range [][3]string{{"mf-admin", "itest-mf1", "admin"}, {"mf-bob", "itest-mf1", "viewer"}, {"mf-plain", "itest-mf1", "viewer"}, {"mf-admin2", "itest-mf2", "admin"}} {
		pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role,password_hash) VALUES($1,$2,$1||'@mf-test.example','U',$3,$4)`, u[0], u[1], u[2], hash)
	}
	secret := totp.NewSecret()
	blob, _ := secrets.Seal(key, "itest-mf1", totpName("mf-bob"), secret)
	pool.Exec(ctx, `INSERT INTO user_totp(user_id,tenant_id,secret,confirmed) VALUES('mf-bob','itest-mf1',$1,true)`, blob)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/login", s.localLogin)
	mux.HandleFunc("DELETE /v1/users/{id}/totp", s.resetUserTOTP)
	h := s.activeUser(mux)
	login := func(user, code string) *httptest.ResponseRecorder {
		body := `{"email":"` + user + `@mf-test.example","password":"` + pw + `"`
		if code != "" {
			body += `,"code":"` + code + `"`
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/auth/login", strings.NewReader(body+`}`)))
		return w
	}
	// a user without an authenticator signs in as before
	if w := login("mf-plain", ""); w.Code != 200 {
		t.Fatalf("plain login = %d", w.Code)
	}
	// password alone is not enough once enrolled, and the response says so without a token
	w := login("mf-bob", "")
	if w.Code != 401 || !strings.Contains(w.Body.String(), `"mfa_required":true`) || strings.Contains(w.Body.String(), "token") {
		t.Fatalf("no code = %d %s", w.Code, w.Body.String())
	}
	if w := login("mf-bob", "000000"); w.Code != 401 {
		t.Fatalf("wrong code = %d", w.Code)
	}
	cur := totp.Step(time.Now())
	good := totp.CodeAt(secret, cur)
	if w := login("mf-bob", good); w.Code != 200 || !strings.Contains(w.Body.String(), `"token"`) {
		t.Fatalf("good code = %d %s", w.Code, w.Body.String())
	}
	// a code works once
	if w := login("mf-bob", good); w.Code != 401 {
		t.Fatalf("replayed code = %d", w.Code)
	}
	// repeated wrong codes lock the account, even for a later right code
	for i := 0; i < maxFailedLogins+1; i++ {
		login("mf-bob", "111111")
	}
	if w := login("mf-bob", totp.CodeAt(secret, cur+1)); w.Code != 429 {
		t.Fatalf("locked account with a right code = %d, want 429", w.Code)
	}
	pool.Exec(ctx, `UPDATE users SET failed_logins=0, locked_until=NULL WHERE id='mf-bob'`)
	// reset rules: admin only, own tenant only, not self
	del := func(tenant, actor, role, id string) int {
		return callAs(h, tenant, actor, role, "DELETE", "/v1/users/"+id+"/totp", "").Code
	}
	if c := del("itest-mf1", "mf-plain", "viewer", "mf-bob"); c != 403 {
		t.Fatalf("viewer reset = %d", c)
	}
	if c := del("itest-mf2", "mf-admin2", "admin", "mf-bob"); c != 404 {
		t.Fatalf("other tenant's admin reset = %d, want 404", c)
	}
	if c := del("itest-mf1", "mf-bob", "admin", "mf-bob"); c == 204 {
		t.Fatalf("self reset allowed")
	}
	if c := del("itest-mf1", "mf-admin", "admin", "mf-bob"); c != 204 {
		t.Fatalf("admin reset = %d", c)
	}
	if w := login("mf-bob", ""); w.Code != 200 {
		t.Fatalf("login after reset = %d", w.Code)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id='itest-mf1' AND action='user.totp_reset'`).Scan(&n)
	if n != 1 {
		t.Fatalf("reset audit rows = %d", n)
	}
}
