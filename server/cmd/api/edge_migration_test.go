package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/pki"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func migrationTestCSR(t *testing.T, serial string) string {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	b, e := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: serial}}, key)
	if e != nil {
		t.Fatal(e)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: b}))
}
func TestMigrationTransportDoesNotTrustArbitraryForwarding(t *testing.T) {
	for _, x := range []struct {
		remote, proxy, forward string
		secure, want           bool
	}{{"192.0.2.1:123", "", "https", false, false}, {"192.0.2.1:123", "192.0.2.2", "https", false, false}, {"192.0.2.1:123", "192.0.2.1", "https", false, true}, {"192.0.2.1:123", "", "", true, true}, {"127.0.0.1:123", "", "", false, true}} {
		t.Setenv("EDGE_HTTPS_PROXY_IP", x.proxy)
		r := httptest.NewRequest("POST", "/", nil)
		r.RemoteAddr = x.remote
		r.Header.Set("X-Forwarded-Proto", x.forward)
		if x.secure {
			r.TLS = &tls.ConnectionState{}
		}
		if got := migrationTransport(httptest.NewRecorder(), r); got != x.want {
			t.Fatal(x)
		}
	}
}
func TestMigrationCSRRejectsMismatchedOrUnsupportedMaterial(t *testing.T) {
	csr := migrationTestCSR(t, "SERIAL")
	if _, _, e := migrationCSR(csr, "SERIAL"); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{"", csr + csr, strings.Repeat("x", 17000)} {
		if _, _, e := migrationCSR(raw, "SERIAL"); e == nil {
			t.Fatal("accepted bad CSR")
		}
	}
	if _, _, e := migrationCSR(csr, "OTHER"); e == nil {
		t.Fatal("serial mismatch")
	}
}
func TestIntegrationIdentityMigrationIsExplicitOwnGatewayAndRecoverable(t *testing.T) {
	s, _ := testServer(t)
	tenant := "itest-migration"
	seed(t, s, tenant)
	var serial string
	if e := s.st.Pool.QueryRow(context.Background(), `SELECT serial FROM gateways WHERE id=$1`, tenant+"-gw").Scan(&serial); e != nil {
		t.Fatal(e)
	}
	_, _ = s.st.Pool.Exec(context.Background(), `UPDATE gateways SET cert_fingerprint=NULL WHERE id=$1`, tenant+"-gw")
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/edge/self/identity-migrations", s.requestIdentityMigration)
	mux.HandleFunc("GET /v1/edge/self/identity-migrations/{id}", s.getIdentityMigration)
	mux.HandleFunc("GET /v1/edge/identity-migrations", s.listIdentityMigrations)
	mux.HandleFunc("POST /v1/edge/identity-migrations/{id}/approve", s.approveIdentityMigration)
	mux.HandleFunc("POST /v1/edge/identity-migrations/{id}/revoke", s.revokePendingIdentityMigration)
	run := func(role, principal, owner, method, path, body string, key bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.TLS = &tls.ConnectionState{}
		c := context.WithValue(r.Context(), auth.CtxTenant, owner)
		c = context.WithValue(c, auth.CtxRole, role)
		c = context.WithValue(c, auth.CtxUser, principal)
		if key {
			c = context.WithValue(c, auth.CtxViaKey, true)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r.WithContext(c))
		return w
	}
	csr := migrationTestCSR(t, serial)
	b, _ := json.Marshal(map[string]any{"csr_pem": csr})
	body := string(b)
	path := "/v1/edge/self/identity-migrations"
	if w := run("gateway", tenant+"-gw", tenant, "POST", path, body, true); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := run("gateway", tenant+"-gw", "foreign", "POST", path, body, false); w.Code != 404 {
		t.Fatal(w.Code)
	}
	w := run("gateway", tenant+"-gw", tenant, "POST", path, body, false)
	if w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	id := out["id"].(string)
	if w := run("gateway", tenant+"-gw", tenant, "POST", path, body, false); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	checkFP := func(want string) {
		t.Helper()
		var fp string
		s.st.Pool.QueryRow(context.Background(), `SELECT COALESCE(cert_fingerprint,'') FROM gateways WHERE id=$1`, tenant+"-gw").Scan(&fp)
		if fp != want {
			t.Fatalf("fingerprint %q want %q", fp, want)
		}
	}
	checkFP("")
	approve := "/v1/edge/identity-migrations/" + id + "/approve"
	payload, _ := json.Marshal(map[string]any{"key_sha256": out["key_sha256"], "csr_sha256": out["csr_sha256"], "compared_local_fingerprint": true})
	if w := run("admin", "test-user", tenant, "POST", approve, string(payload), true); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := run("operator", "test-user", tenant, "POST", approve, string(payload), false); w.Code != 403 {
		t.Fatal(w.Code)
	}
	checkFP("")
	dir := t.TempDir()
	ca, key, e := pki.GenerateCA("Synthetic migration CA", 2)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(dir, "ca.crt"), ca, 0600)
	os.WriteFile(filepath.Join(dir, "ca.key"), key, 0600)
	t.Setenv("MTLS_CA_CERT", filepath.Join(dir, "ca.crt"))
	t.Setenv("MTLS_CA_KEY", filepath.Join(dir, "ca.key"))
	t.Setenv("EDGE_MQTT_TLS", "true")
	t.Setenv("EDGE_MQTT_PORT", "8883")
	t.Setenv("EDGE_MQTT_HOST", "mqtt.example.invalid")
	t.Setenv("EDGE_MQTT_CA_FILE", filepath.Join(dir, "ca.crt"))
	bad, _ := json.Marshal(map[string]any{"key_sha256": "wrong", "csr_sha256": out["csr_sha256"], "compared_local_fingerprint": true})
	if w := run("admin", "test-user", tenant, "POST", approve, string(bad), false); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	checkFP("")
	// An admin may rename/reassign the gateway after a request was staged.
	// The old public CSR must not issue a certificate for its stale serial.
	if _, err := s.st.Pool.Exec(t.Context(), `UPDATE gateways SET serial=$1 WHERE id=$2`, serial+"-renamed", tenant+"-gw"); err != nil {
		t.Fatal(err)
	}
	if w := run("admin", "test-user", tenant, "POST", approve, string(payload), false); w.Code != 404 {
		t.Fatal("stale serial approval", w.Code, w.Body)
	}
	checkFP("")
	if _, err := s.st.Pool.Exec(t.Context(), `UPDATE gateways SET serial=$1 WHERE id=$2`, serial, tenant+"-gw"); err != nil {
		t.Fatal(err)
	}
	w = run("admin", "test-user", tenant, "POST", approve, string(payload), false)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var fp string
	s.st.Pool.QueryRow(context.Background(), `SELECT cert_fingerprint FROM gateways WHERE id=$1`, tenant+"-gw").Scan(&fp)
	if fp == "" {
		t.Fatal("no certificate fingerprint")
	}
	if w := run("admin", "test-user", tenant, "POST", approve, string(payload), false); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	checkFP(fp)
	w = run("gateway", tenant+"-gw", tenant, "GET", path+"/"+id, "", false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "client_cert_pem") || strings.Contains(w.Body.String(), "PRIVATE KEY") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := run("gateway", "other-gateway", tenant, "GET", path+"/"+id, "", false); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := run("admin", "test-user", tenant, "POST", "/v1/edge/identity-migrations/"+id+"/revoke", "", false); w.Code != 409 {
		t.Fatal("issued certificate revoke was implicit", w.Code)
	}
	_, _ = s.st.Pool.Exec(context.Background(), `UPDATE edge_identity_migrations SET expires_at=now()-interval '1 second' WHERE id=$1`, id)
	if w := run("gateway", tenant+"-gw", tenant, "GET", path+"/"+id, "", false); w.Code != 409 {
		t.Fatal(w.Code)
	}
}
