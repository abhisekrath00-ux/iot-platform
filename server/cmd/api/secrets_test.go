package main

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/secrets"
)

func TestIntegrationSecrets(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-sec1")
	seed(t, s, "itest-sec2")
	ctx := context.Background()
	s.st.Pool.Exec(ctx, `DELETE FROM secrets WHERE tenant_id IN ('itest-sec1','itest-sec2')`)
	t.Cleanup(func() { s.st.Pool.Exec(ctx, `DELETE FROM secrets WHERE tenant_id IN ('itest-sec1','itest-sec2')`) })
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/secrets", s.listSecrets)
	api.HandleFunc("PUT /v1/secrets/{name}", s.putSecret)
	api.HandleFunc("DELETE /v1/secrets/{name}", s.deleteSecret)

	// disabled without a key
	if w := call(api, "itest-sec1", "admin", "GET", "/v1/secrets", ""); w.Code != 503 {
		t.Fatalf("no key = %d, want 503", w.Code)
	}
	s.secrets = &secrets.Store{Pool: s.st.Pool, Key: bytes.Repeat([]byte{7}, 32)}
	if w := call(api, "itest-sec1", "operator", "PUT", "/v1/secrets/api-token", `{"value":"x"}`); w.Code != 403 {
		t.Fatalf("operator put = %d, want 403", w.Code)
	}
	if w := call(api, "itest-sec1", "admin", "PUT", "/v1/secrets/Bad%20Name", `{"value":"x"}`); w.Code != 400 && w.Code != 404 {
		t.Fatalf("bad name = %d", w.Code)
	}
	if w := call(api, "itest-sec1", "admin", "PUT", "/v1/secrets/api-token", `{"value":"s3cr3t-value"}`); w.Code != 200 {
		t.Fatalf("put = %d %s", w.Code, w.Body.String())
	}
	if w := call(api, "itest-sec1", "admin", "PUT", "/v1/secrets/api-token", `{"value":""}`); w.Code != 400 {
		t.Fatalf("empty value = %d", w.Code)
	}
	w := call(api, "itest-sec1", "admin", "GET", "/v1/secrets", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "api-token") || strings.Contains(w.Body.String(), "s3cr3t") {
		t.Fatalf("list leaks or fails: %d %s", w.Code, w.Body.String())
	}
	// stored encrypted, readable only by the server with the key, per tenant
	var raw []byte
	s.st.Pool.QueryRow(ctx, `SELECT ciphertext FROM secrets WHERE tenant_id='itest-sec1' AND name='api-token'`).Scan(&raw)
	if bytes.Contains(raw, []byte("s3cr3t")) {
		t.Fatal("plaintext in database")
	}
	if v, err := s.secrets.Get(ctx, "itest-sec1", "api-token"); err != nil || string(v) != "s3cr3t-value" {
		t.Fatalf("get = %q %v", v, err)
	}
	if _, err := s.secrets.Get(ctx, "itest-sec2", "api-token"); err != secrets.ErrNotFound {
		t.Fatalf("other tenant get = %v", err)
	}
	if w := call(api, "itest-sec2", "admin", "GET", "/v1/secrets", ""); strings.Contains(w.Body.String(), "api-token") {
		t.Fatal("tenant 2 sees tenant 1 secret name")
	}
	// ciphertext moved to another tenant's row must not decrypt
	s.st.Pool.Exec(ctx, `INSERT INTO secrets(tenant_id,name,ciphertext,created_by) VALUES('itest-sec2','api-token',$1,'test-user')`, raw)
	if _, err := s.secrets.Get(ctx, "itest-sec2", "api-token"); err == nil {
		t.Fatal("copied ciphertext decrypted under another tenant")
	}
	// the secret value never appears in the audit log
	var n int
	s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id='itest-sec1' AND action='secret.put' AND detail::text ILIKE '%s3cr3t%'`).Scan(&n)
	if n != 0 {
		t.Fatal("secret value in audit log")
	}
	if w := call(api, "itest-sec1", "admin", "DELETE", "/v1/secrets/api-token", ""); w.Code != 200 {
		t.Fatalf("delete = %d", w.Code)
	}
	if w := call(api, "itest-sec1", "admin", "DELETE", "/v1/secrets/api-token", ""); w.Code != 404 {
		t.Fatalf("second delete = %d", w.Code)
	}
}
