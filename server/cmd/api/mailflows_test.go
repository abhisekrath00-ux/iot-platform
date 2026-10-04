package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

type fakeMail struct {
	mu   sync.Mutex
	sent []struct{ to, subject, body string }
}

func (f *fakeMail) Email(_ context.Context, to []string, subject, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, struct{ to, subject, body string }{strings.Join(to, ","), subject, body})
	return nil
}
func (f *fakeMail) Slack(context.Context, string, string) error { return nil }
func (f *fakeMail) last(to string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.sent) - 1; i >= 0; i-- {
		if f.sent[i].to == to {
			return f.sent[i].body, true
		}
	}
	return "", false
}
func (f *fakeMail) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.sent) }

func TestIntegrationMailFlows(t *testing.T) {
	t.Setenv("LOCAL_LOGIN", "1")
	t.Setenv("TEST_MAIL", "1")
	t.Setenv("APP_PUBLIC_URL", "https://iot.example.test/")
	s, _ := testServer(t)
	s.secret = []byte("test-secret-test-secret-test-secret")
	fm := &fakeMail{}
	s.notifier = fm
	seed(t, s, "itest-ml1")
	ctx := t.Context()
	pool := s.st.Pool
	clean := func() {
		pool.Exec(ctx, `DELETE FROM password_resets WHERE tenant_id='itest-ml1'`)
		pool.Exec(ctx, `DELETE FROM user_invites WHERE tenant_id='itest-ml1'`)
		pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id='itest-ml1'`)
		pool.Exec(ctx, `DELETE FROM users WHERE tenant_id='itest-ml1'`)
	}
	clean()
	t.Cleanup(clean)
	const pw = "a-long-enough-password"
	hash, _ := auth.HashPassword(pw)
	pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role,password_hash) VALUES('ml-admin','itest-ml1','ml-admin@ml-test.example','A','admin',$1),('ml-bob','itest-ml1','bob@ml-test.example','B','viewer',$1)`, hash)
	pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role,password_hash,disabled_at) VALUES('ml-off','itest-ml1','off@ml-test.example','O','viewer',$1,now())`, hash)
	pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('ml-sso','itest-ml1','sso@ml-test.example','S','viewer')`)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/users/invites", s.createInvite)
	mux.HandleFunc("POST /auth/forgot", s.forgotPassword)
	mux.HandleFunc("POST /auth/reset", s.resetPassword)
	mux.HandleFunc("POST /auth/login", s.localLogin)
	h := s.activeUser(mux)
	post := func(path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(body)))
		return w
	}
	wait := func(to string) string {
		for i := 0; i < 50; i++ {
			if b, ok := fm.last(to); ok {
				return b
			}
			time.Sleep(20 * time.Millisecond)
		}
		return ""
	}
	// an invitation can be emailed; the link uses the configured address, not a request header
	w := callAs(h, "itest-ml1", "ml-admin", "admin", "POST", "/v1/users/invites", `{"email":"new@ml-test.example","role":"viewer","send_email":true}`)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"emailed":true`) {
		t.Fatalf("invite = %d %s", w.Code, w.Body.String())
	}
	if b := wait("new@ml-test.example"); !strings.Contains(b, "https://iot.example.test/accept-invite#") {
		t.Fatalf("invite mail: %q", b)
	}
	// without the flag, no mail goes out
	n := fm.count()
	callAs(h, "itest-ml1", "ml-admin", "admin", "POST", "/v1/users/invites", `{"email":"quiet@ml-test.example","role":"viewer"}`)
	if fm.count() != n {
		t.Fatal("mail sent without send_email")
	}
	// forgot: identical answer for known, unknown, disabled and SSO-only addresses; mail only to the real one
	var first string
	for _, e := range []string{"bob@ml-test.example", "nobody@ml-test.example", "off@ml-test.example", "sso@ml-test.example"} {
		w := post("/auth/forgot", `{"email":"`+e+`"}`)
		if w.Code != 202 {
			t.Fatalf("forgot %s = %d", e, w.Code)
		}
		if first == "" {
			first = w.Body.String()
		} else if w.Body.String() != first {
			t.Fatalf("forgot answers differ: %q vs %q", first, w.Body.String())
		}
	}
	body := wait("bob@ml-test.example")
	m := regexp.MustCompile(`https://iot.example.test/reset-password#([A-Za-z0-9_-]+)`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no reset link in %q", body)
	}
	time.Sleep(100 * time.Millisecond)
	for _, e := range []string{"nobody@ml-test.example", "off@ml-test.example", "sso@ml-test.example"} {
		if _, ok := fm.last(e); ok {
			t.Fatalf("reset mail sent to %s", e)
		}
	}
	token := m[1]
	if w := post("/auth/reset", `{"token":"`+token+`","password":"short"}`); w.Code != 400 {
		t.Fatalf("weak password = %d", w.Code)
	}
	if w := post("/auth/reset", `{"token":"nope","password":"`+pw+`-new"}`); w.Code != 400 {
		t.Fatalf("bad token = %d", w.Code)
	}
	// lock the account first: a reset also clears the lockout
	pool.Exec(ctx, `UPDATE users SET failed_logins=9, locked_until=now()+interval '1 hour' WHERE id='ml-bob'`)
	if w := post("/auth/reset", `{"token":"`+token+`","password":"`+pw+`-new"}`); w.Code != 204 {
		t.Fatalf("reset = %d %s", w.Code, w.Body.String())
	}
	if w := post("/auth/reset", `{"token":"`+token+`","password":"`+pw+`-other"}`); w.Code != 400 {
		t.Fatalf("reused link = %d, want 400", w.Code)
	}
	if w := post("/auth/login", `{"email":"bob@ml-test.example","password":"`+pw+`"}`); w.Code != 401 {
		t.Fatalf("old password after reset = %d", w.Code)
	}
	if w := post("/auth/login", `{"email":"bob@ml-test.example","password":"`+pw+`-new"}`); w.Code != 200 {
		t.Fatalf("new password = %d", w.Code)
	}
	// an expired link is refused
	post("/auth/forgot", `{"email":"bob@ml-test.example"}`)
	pool.Exec(ctx, `UPDATE password_resets SET expires_at=now()-interval '1 minute' WHERE user_id='ml-bob'`)
	var tok2 string
	pool.QueryRow(ctx, `SELECT count(*)::text FROM password_resets WHERE user_id='ml-bob' AND used_at IS NULL`).Scan(&tok2)
	b2 := wait("bob@ml-test.example")
	if m2 := regexp.MustCompile(`#([A-Za-z0-9_-]+)`).FindStringSubmatch(b2); m2 != nil {
		if w := post("/auth/reset", `{"token":"`+m2[1]+`","password":"`+pw+`-third"}`); w.Code != 400 {
			t.Fatalf("expired link = %d", w.Code)
		}
	}
	// off by default: with mail not configured the endpoints do not exist
	t.Setenv("TEST_MAIL", "")
	if w := post("/auth/forgot", `{"email":"bob@ml-test.example"}`); w.Code != 404 {
		t.Fatalf("forgot with no mail configured = %d, want 404", w.Code)
	}
	if w := callAs(h, "itest-ml1", "ml-admin", "admin", "POST", "/v1/users/invites", `{"email":"x@ml-test.example","role":"viewer","send_email":true}`); w.Code != 409 {
		t.Fatalf("invite email with no mail configured = %d, want 409", w.Code)
	}
}
