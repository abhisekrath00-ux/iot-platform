package main

import (
	"context"
	"encoding/base32"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/secrets"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/totp"
)

func TestIntegrationTOTPApproval(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-tp")
	ctx := t.Context()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 3)
	}
	s.secrets = &secrets.Store{Pool: s.st.Pool, Key: key}
	clean := func() {
		s.st.Pool.Exec(ctx, `DELETE FROM user_totp WHERE tenant_id='itest-tp'`)
		s.st.Pool.Exec(ctx, `DELETE FROM tenant_features WHERE tenant_id='itest-tp'`)
		s.st.Pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id='itest-tp'`)
	}
	clean()
	t.Cleanup(clean)
	s.st.Pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('itest-tp-op','itest-tp','itest-tp-op@example.test','Op','operator') ON CONFLICT DO NOTHING`)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/me/totp", s.totpStatus)
	mux.HandleFunc("POST /v1/me/totp/begin", s.totpBegin)
	mux.HandleFunc("POST /v1/me/totp/confirm", s.totpConfirm)
	mux.HandleFunc("DELETE /v1/me/totp", s.totpRemove)
	mux.HandleFunc("POST /v1/commands/{id}/approve", s.approveCommand)
	mux.HandleFunc("PUT /v1/features/{feature}", s.putFeature)
	as := func(user, role, method, path, body string, viaKey bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		c := context.WithValue(r.Context(), auth.CtxTenant, "itest-tp")
		c = context.WithValue(c, auth.CtxUser, user)
		c = context.WithValue(c, auth.CtxRole, role)
		if viaKey {
			c = context.WithValue(c, auth.CtxViaKey, true)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r.WithContext(c))
		return w
	}
	const op = "itest-tp-op"
	approve := func(code string) int {
		return as(op, "operator", "POST", "/v1/commands/none/approve", `{"code":"`+code+`"}`, false).Code
	}
	// policy off: no code needed (the command does not exist, so 409 is the "got past the checks" answer)
	if c := approve(""); c != 409 {
		t.Fatalf("policy off: %d", c)
	}
	if as("test-user", "admin", "PUT", "/v1/features/"+featureTOTP, `{"enabled":true}`, false).Code != 200 {
		t.Fatal("enable policy")
	}
	if c := approve("123456"); c != 403 {
		t.Fatalf("policy on without enrollment: %d", c)
	}
	// enroll: not active until confirmed
	if as(op, "operator", "POST", "/v1/me/totp/begin", "", true).Code != 403 {
		t.Fatal("an API key must not enroll")
	}
	w := as(op, "operator", "POST", "/v1/me/totp/begin", "", false)
	var b struct{ Secret, URI string }
	json.Unmarshal(w.Body.Bytes(), &b)
	if w.Code != 200 || b.Secret == "" || !strings.HasPrefix(b.URI, "otpauth://totp/") {
		t.Fatalf("begin: %d %s", w.Code, w.Body.String())
	}
	var stored string
	s.st.Pool.QueryRow(ctx, `SELECT encode(secret,'escape') FROM user_totp WHERE user_id=$1`, op).Scan(&stored)
	if strings.Contains(stored, b.Secret) {
		t.Fatal("the secret must be stored sealed")
	}
	raw, _ := totpDecodeB32(b.Secret)
	if c := approve(totp.CodeAt(raw, totp.Step(time.Now()))); c != 403 {
		t.Fatalf("an unconfirmed factor must not approve: %d", c)
	}
	if as(op, "operator", "POST", "/v1/me/totp/confirm", `{"code":"000000"}`, false).Code != 400 {
		t.Fatal("wrong confirm code accepted")
	}
	code := totp.CodeAt(raw, totp.Step(time.Now()))
	if as(op, "operator", "POST", "/v1/me/totp/confirm", `{"code":"`+code+`"}`, false).Code != 200 {
		t.Fatal("confirm failed")
	}
	// the code that confirmed enrollment is spent; the next step's code works once
	if c := approve(code); c != 403 {
		t.Fatalf("a spent code was accepted: %d", c)
	}
	next := totp.CodeAt(raw, totp.Step(time.Now())+1)
	if c := approve(next); c != 409 {
		t.Fatalf("a valid code should get past the check: %d", c)
	}
	if c := approve(next); c != 403 {
		t.Fatalf("replay of a used code: %d", c)
	}
	if c := approve("999999"); c != 403 {
		t.Fatalf("wrong code: %d", c)
	}
	// removing the factor needs a current code too
	if as(op, "operator", "DELETE", "/v1/me/totp", `{"code":"000000"}`, false).Code != 400 {
		t.Fatal("removed with a bad code")
	}
	var cnt int
	s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id='itest-tp' AND action='command.approve_refused'`).Scan(&cnt)
	if cnt != 3 {
		t.Fatalf("refusals should be audited: %d", cnt)
	}
}

func totpDecodeB32(s string) ([]byte, error) {
	return base32NoPad.DecodeString(s)
}

var base32NoPad = base32.StdEncoding.WithPadding(base32.NoPadding)
