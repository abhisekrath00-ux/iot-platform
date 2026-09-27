package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func token(t *testing.T, secret []byte, tenant, role string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		TenantID: tenant, Role: role,
		RegisteredClaims: jwt.RegisteredClaims{Subject: "u1", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
	})
	s, err := tok.SignedString(secret)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMiddleware(t *testing.T) {
	secret := []byte("test-secret-32-bytes-minimum-xxxx")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if Tenant(r) != "acme" || Role(r) != "admin" || User(r) != "u1" {
			t.Errorf("claims not propagated: %s %s %s", Tenant(r), Role(r), User(r))
		}
		w.WriteHeader(200)
	})
	h := Middleware(secret)(next)

	r := httptest.NewRequest("GET", "/v1/devices", nil)
	r.Header.Set("Authorization", "Bearer "+token(t, secret, "acme", "admin"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("valid token rejected: %d", w.Code)
	}

	// no token
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/devices", nil))
	if w.Code != 401 {
		t.Fatalf("missing token: got %d want 401", w.Code)
	}

	// wrong secret
	r = httptest.NewRequest("GET", "/v1/devices", nil)
	r.Header.Set("Authorization", "Bearer "+token(t, []byte("other-secret-32-bytes-minimumxx"), "acme", "admin"))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("wrong secret: got %d want 401", w.Code)
	}

	// empty tenant claim must reject
	r = httptest.NewRequest("GET", "/v1/devices", nil)
	r.Header.Set("Authorization", "Bearer "+token(t, secret, "", "admin"))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("empty tenant: got %d want 401", w.Code)
	}
}
