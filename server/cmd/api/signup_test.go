package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIntegrationSelfSignup(t *testing.T) {
	s, _ := testServer(t)
	s.secret = []byte("test-secret-test-secret-test-secret")
	ctx := t.Context()
	pool := s.st.Pool
	clean := func() {
		pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id LIKE 'signup-test-%'`)
		pool.Exec(ctx, `DELETE FROM users WHERE email ILIKE '%@signup-test.example'`)
		pool.Exec(ctx, `DELETE FROM tenants WHERE id LIKE 'signup-test-%'`)
	}
	clean()
	t.Cleanup(clean)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/signup", s.selfSignup)
	mux.HandleFunc("GET /auth/config", s.authConfig)
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/auth/signup", strings.NewReader(body)))
		return w
	}
	const pw = "a-long-enough-password"
	body := func(ws, email string) string {
		return `{"workspace":"` + ws + `","email":"` + email + `","password":"` + pw + `"}`
	}
	// off by default, even with local sign-in on
	t.Setenv("LOCAL_LOGIN", "1")
	t.Setenv("SELF_SIGNUP", "")
	if w := post(body("signup-test-a", "a@signup-test.example")); w.Code != 404 {
		t.Fatalf("disabled sign-up = %d, want 404", w.Code)
	}
	if w := httptest.NewRecorder(); true {
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/auth/config", nil))
		if !strings.Contains(w.Body.String(), `"self_signup":false`) {
			t.Fatalf("config: %s", w.Body.String())
		}
	}
	// on, but only with local sign-in
	t.Setenv("SELF_SIGNUP", "1")
	t.Setenv("LOCAL_LOGIN", "")
	if w := post(body("signup-test-a", "a@signup-test.example")); w.Code != 404 {
		t.Fatalf("sign-up without local sign-in = %d, want 404", w.Code)
	}
	t.Setenv("LOCAL_LOGIN", "1")
	var tenants int
	pool.QueryRow(ctx, `SELECT count(*) FROM tenants`).Scan(&tenants)
	t.Setenv("SELF_SIGNUP_MAX_TENANTS", fmt.Sprint(tenants+3))
	w := post(body("signup-test-a", "a@signup-test.example"))
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"role":"admin"`) || !strings.Contains(w.Body.String(), `"token"`) {
		t.Fatalf("sign-up = %d %s", w.Code, w.Body.String())
	}
	var role, tid string
	pool.QueryRow(ctx, `SELECT role, tenant_id FROM users WHERE email='a@signup-test.example'`).Scan(&role, &tid)
	if role != "admin" || !strings.HasPrefix(tid, "signup-test-a-") {
		t.Fatalf("created %q in %q", role, tid)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id=$1 AND action='tenant.self_signup'`, tid).Scan(&n)
	if n != 1 {
		t.Fatal("sign-up is not audited")
	}
	if w := post(body("another", "A@Signup-Test.example")); w.Code != 409 {
		t.Fatalf("duplicate email (other case) = %d, want 409", w.Code)
	}
	for _, c := range []string{
		`{"workspace":"","email":"b@signup-test.example","password":"` + pw + `"}`,
		`{"workspace":"x","email":"not-an-email","password":"` + pw + `"}`,
		`{"workspace":"x","email":"c@signup-test.example","password":"short"}`,
		`{"workspace":"x","email":"d@signup-test.example","password":"` + pw + `","role":"admin"` + `,`,
	} {
		if w := post(c); w.Code != 400 {
			t.Errorf("bad request %q = %d, want 400", c, w.Code)
		}
	}
	// a workspace name cannot choose or collide with another tenant's id
	if w := post(body("itest", "e@signup-test.example")); w.Code != 201 {
		t.Fatalf("name resembling another tenant = %d", w.Code)
	}
	var other string
	pool.QueryRow(ctx, `SELECT tenant_id FROM users WHERE email='e@signup-test.example'`).Scan(&other)
	if other == "itest" || !strings.HasPrefix(other, "itest-") || len(other) != len("itest-")+6 {
		t.Fatalf("tenant id %q", other)
	}
	pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id=$1`, other)
	pool.Exec(ctx, `DELETE FROM users WHERE tenant_id=$1`, other)
	pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, other)
	// the tenant cap closes sign-up
	pool.QueryRow(ctx, `SELECT count(*) FROM tenants`).Scan(&tenants)
	t.Setenv("SELF_SIGNUP_MAX_TENANTS", fmt.Sprint(tenants))
	if w := post(body("signup-test-z", "z@signup-test.example")); w.Code != 403 {
		t.Fatalf("over the cap = %d, want 403", w.Code)
	}
}
