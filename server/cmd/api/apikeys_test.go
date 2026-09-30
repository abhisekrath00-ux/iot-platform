package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIntegrationAPIKeys(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-ak1")
	seed(t, s, "itest-ak2")
	s.st.Pool.Exec(t.Context(), `DELETE FROM api_keys WHERE tenant_id IN ('itest-ak1','itest-ak2')`)
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/devices", s.listDevices)
	api.HandleFunc("GET /v1/api-keys", s.listAPIKeys)
	api.HandleFunc("POST /v1/api-keys", s.createAPIKey)
	api.HandleFunc("DELETE /v1/api-keys/{id}", s.revokeAPIKey)

	if w := call(api, "itest-ak1", "operator", "POST", "/v1/api-keys", `{"name":"x","expires_in_days":30}`); w.Code != 403 {
		t.Fatalf("operator create = %d", w.Code)
	}
	for _, bad := range []string{`{"name":"","expires_in_days":30}`, `{"name":"k","role":"admin","expires_in_days":30}`, `{"name":"k","expires_in_days":0}`, `{"name":"k","expires_in_days":400}`} {
		if w := call(api, "itest-ak1", "admin", "POST", "/v1/api-keys", bad); w.Code != 400 {
			t.Fatalf("%s = %d, want 400", bad, w.Code)
		}
	}
	w := call(api, "itest-ak1", "admin", "POST", "/v1/api-keys", `{"name":"scada","role":"viewer","expires_in_days":30}`)
	if w.Code != 201 {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	var created struct{ ID, Token string }
	json.Unmarshal(w.Body.Bytes(), &created)
	if !strings.HasPrefix(created.Token, "hxk_") {
		t.Fatalf("token %q", created.Token)
	}
	// secret is never stored or listed
	if l := call(api, "itest-ak1", "admin", "GET", "/v1/api-keys", ""); strings.Contains(l.Body.String(), created.Token) || strings.Contains(l.Body.String(), "secret") {
		t.Fatalf("list leaks secret: %s", l.Body.String())
	}
	if l := call(api, "itest-ak2", "admin", "GET", "/v1/api-keys", ""); strings.Contains(l.Body.String(), created.ID) {
		t.Fatal("cross-tenant list leak")
	}

	guarded := auth_mw(s, api)
	use := func(tok, method, path string) int {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		guarded.ServeHTTP(rec, r)
		return rec.Code
	}
	if c := use(created.Token, "GET", "/v1/devices"); c != 200 {
		t.Fatalf("key read = %d", c)
	}
	// keys cannot manage keys, even an operator key
	if c := use(created.Token, "GET", "/v1/api-keys"); c != 403 {
		t.Fatalf("key listing keys = %d, want 403", c)
	}
	// tampered secret and unknown id fail
	if c := use(created.Token+"x", "GET", "/v1/devices"); c != 401 {
		t.Fatalf("bad secret = %d", c)
	}
	if c := use("hxk_deadbeef.abc", "GET", "/v1/devices"); c != 401 {
		t.Fatalf("unknown key = %d", c)
	}
	// other tenant cannot revoke; owner can; revoked key stops working
	if w := call(api, "itest-ak2", "admin", "DELETE", "/v1/api-keys/"+created.ID, ""); w.Code != 404 {
		t.Fatalf("cross-tenant revoke = %d", w.Code)
	}
	if w := call(api, "itest-ak1", "admin", "DELETE", "/v1/api-keys/"+created.ID, ""); w.Code != 200 {
		t.Fatalf("revoke = %d", w.Code)
	}
	if c := use(created.Token, "GET", "/v1/devices"); c != 401 {
		t.Fatalf("revoked key = %d, want 401", c)
	}
	// expired key
	w = call(api, "itest-ak1", "admin", "POST", "/v1/api-keys", `{"name":"old","expires_in_days":1}`)
	json.Unmarshal(w.Body.Bytes(), &created)
	s.st.Pool.Exec(t.Context(), `UPDATE api_keys SET expires_at=now()-interval '1 minute' WHERE id=$1`, created.ID)
	if c := use(created.Token, "GET", "/v1/devices"); c != 401 {
		t.Fatalf("expired key = %d, want 401", c)
	}
}

func auth_mw(s *server, h http.Handler) http.Handler {
	return authMiddleware(s.secret, s.resolveAPIKey)(h)
}
