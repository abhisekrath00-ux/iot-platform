package main

import (
	"context"
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
}
