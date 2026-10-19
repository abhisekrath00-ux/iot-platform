package main

// Integration tests for POST /v1/enrollment/claim (real Postgres).
// Regression: the claim used to burn the one-time code and then fail with a
// 500 when the deployment had no mTLS CA configured (the default - compose
// and .env.example ship without MTLS_CA_*). Now the CSR is signed before
// redemption, and a missing CA falls back to a token-only identity.
import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/enroll"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/pki"
	"github.com/google/uuid"
)

func claimCSR(t *testing.T, serial string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: serial},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}

// seedClaim inserts a site, a pending gateway and a redeemable token, and
// returns the claim code and gateway id.
func seedClaim(t *testing.T, s *server, tenant, serial string) (code, gwID string) {
	t.Helper()
	ctx := context.Background()
	// serials are globally unique; clear leftovers from earlier failed runs
	s.st.Pool.Exec(ctx, `DELETE FROM enrollment_tokens WHERE serial=$1`, serial)
	s.st.Pool.Exec(ctx, `DELETE FROM gateways WHERE serial=$1`, serial)
	if _, err := s.st.Pool.Exec(ctx,
		`INSERT INTO tenants(id,name) VALUES($1,$1) ON CONFLICT DO NOTHING`, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := s.st.Pool.Exec(ctx,
		`INSERT INTO sites(id,tenant_id,name) VALUES ($1,$2,$3) ON CONFLICT (id) DO NOTHING`,
		tenant+"-site", tenant, "Claim Site"); err != nil {
		t.Fatal(err)
	}
	gwID = uuid.NewString()
	if _, err := s.st.Pool.Exec(ctx,
		`INSERT INTO gateways(id,tenant_id,site_id,serial,status,kind) VALUES ($1,$2,$3,$4,'pending','edge')`,
		gwID, tenant, tenant+"-site", serial); err != nil {
		t.Fatal(err)
	}
	code, err := enroll.NewCode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.st.Pool.Exec(ctx,
		`INSERT INTO enrollment_tokens(id,tenant_id,site_id,gateway_id,serial,code_hash,expires_at,created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		uuid.NewString(), tenant, tenant+"-site", gwID, serial, enroll.Hash(code), time.Now().Add(time.Hour), "claim-test"); err != nil {
		t.Fatal(err)
	}
	return code, gwID
}

func callClaim(t *testing.T, s *server, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/enrollment/claim", s.claimEnrollment)
	req := httptest.NewRequest("POST", "/v1/enrollment/claim", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func claimRedeemed(t *testing.T, s *server, code string) bool {
	t.Helper()
	var claimedAt *time.Time
	if err := s.st.Pool.QueryRow(context.Background(),
		`SELECT claimed_at FROM enrollment_tokens WHERE code_hash=$1`, enroll.Hash(code)).Scan(&claimedAt); err != nil {
		t.Fatal(err)
	}
	return claimedAt != nil
}

func TestIntegrationClaimLabModeWithoutCAFallsBackToToken(t *testing.T) {
	s, _ := testServer(t)
	s.secret = []byte("claim-test-secret-claim-test-secret-42")
	ctx := context.Background()
	defer s.st.Pool.Exec(ctx, `DELETE FROM enrollment_tokens WHERE tenant_id='itest-claim1'`)
	defer s.st.Pool.Exec(ctx, `DELETE FROM gateways WHERE tenant_id='itest-claim1'`)
	defer s.st.Pool.Exec(ctx, `DELETE FROM sites WHERE tenant_id='itest-claim1'`)
	// no MTLS_CA_* and the documented lab posture MQTT_TLS=false: intentional
	// token-only identity, never a silent mTLS downgrade
	t.Setenv("MTLS_CA_CERT", "")
	t.Setenv("MTLS_CA_KEY", "")
	t.Setenv("MQTT_TLS", "false")

	code, gwID := seedClaim(t, s, "itest-claim1", "GW-NOCA")
	body, _ := json.Marshal(map[string]string{
		"claim_code": code, "serial": "GW-NOCA", "csr_pem": claimCSR(t, "GW-NOCA"),
	})
	w := callClaim(t, s, string(body))
	if w.Code != 200 {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["gateway_id"] != gwID || out["ingest_token"] == "" {
		t.Fatalf("identity missing: %v", out)
	}
	if _, ok := out["client_cert_pem"]; ok {
		t.Fatalf("no CA configured but a certificate was issued: %v", out)
	}
	// enforcement invariant: without a fingerprint the gateway is not in the
	// broker ACL (regenerateBrokerACL only lists fingerprinted gateways)
	var dbFP string
	if err := s.st.Pool.QueryRow(ctx, `SELECT COALESCE(cert_fingerprint,'') FROM gateways WHERE id=$1`, gwID).Scan(&dbFP); err != nil {
		t.Fatal(err)
	}
	if dbFP != "" {
		t.Fatalf("token-only identity must not store a fingerprint, got %q", dbFP)
	}
	var status string
	if err := s.st.Pool.QueryRow(ctx, `SELECT status FROM gateways WHERE id=$1`, gwID).Scan(&status); err != nil || status != "active" {
		t.Fatalf("gateway not active: %v %q", err, status)
	}
	// the code is consumed: a second claim gets the generic 403
	w2 := callClaim(t, s, string(body))
	if w2.Code != 403 {
		t.Fatalf("re-claim: status %d want 403", w2.Code)
	}
}

func TestIntegrationClaimBadCSRDoesNotBurnCode(t *testing.T) {
	s, _ := testServer(t)
	s.secret = []byte("claim-test-secret-claim-test-secret-42")
	ctx := context.Background()
	defer s.st.Pool.Exec(ctx, `DELETE FROM enrollment_tokens WHERE tenant_id='itest-claim2'`)
	defer s.st.Pool.Exec(ctx, `DELETE FROM gateways WHERE tenant_id='itest-claim2'`)
	defer s.st.Pool.Exec(ctx, `DELETE FROM sites WHERE tenant_id='itest-claim2'`)
	caCert, caKey, err := pki.GenerateCA("claim-test CA", 1)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	crt, key := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
	os.WriteFile(crt, caCert, 0o600)
	os.WriteFile(key, caKey, 0o600)
	t.Setenv("MTLS_CA_CERT", crt)
	t.Setenv("MTLS_CA_KEY", key)

	code, gwID := seedClaim(t, s, "itest-claim2", "GW-CA")
	// bad CSR: 400 and the code stays redeemable
	bad, _ := json.Marshal(map[string]string{"claim_code": code, "serial": "GW-CA", "csr_pem": "not-a-csr"})
	if w := callClaim(t, s, string(bad)); w.Code != 400 {
		t.Fatalf("bad csr: status %d want 400 (%s)", w.Code, w.Body.String())
	}
	if claimRedeemed(t, s, code) {
		t.Fatal("a rejected CSR burned the one-time code")
	}
	// good CSR: 200 with a certificate and the fingerprint on the gateway row
	good, _ := json.Marshal(map[string]string{
		"claim_code": code, "serial": "GW-CA", "csr_pem": claimCSR(t, "GW-CA"),
	})
	w := callClaim(t, s, string(good))
	if w.Code != 200 {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["client_cert_pem"] == "" || out["ca_cert_pem"] == "" || out["cert_fingerprint"] == "" {
		t.Fatalf("certificate fields missing: %v", out)
	}
	var dbFP string
	if err := s.st.Pool.QueryRow(ctx, `SELECT COALESCE(cert_fingerprint,'') FROM gateways WHERE id=$1`, gwID).Scan(&dbFP); err != nil {
		t.Fatal(err)
	}
	if dbFP == "" || dbFP != fmt.Sprint(out["cert_fingerprint"]) {
		t.Fatalf("fingerprint not stored: row %q resp %v", dbFP, out["cert_fingerprint"])
	}
}

func TestIntegrationClaimNoCAOutsideLabFailsClosed(t *testing.T) {
	s, _ := testServer(t)
	s.secret = []byte("claim-test-secret-claim-test-secret-42")
	ctx := context.Background()
	defer s.st.Pool.Exec(ctx, `DELETE FROM enrollment_tokens WHERE tenant_id='itest-claim3'`)
	defer s.st.Pool.Exec(ctx, `DELETE FROM gateways WHERE tenant_id='itest-claim3'`)
	defer s.st.Pool.Exec(ctx, `DELETE FROM sites WHERE tenant_id='itest-claim3'`)
	// no CA and no lab posture: the claim must fail closed without burning the code
	t.Setenv("MTLS_CA_CERT", "")
	t.Setenv("MTLS_CA_KEY", "")
	t.Setenv("MQTT_TLS", "true")

	code, gwID := seedClaim(t, s, "itest-claim3", "GW-CLOSED")
	body, _ := json.Marshal(map[string]string{
		"claim_code": code, "serial": "GW-CLOSED", "csr_pem": claimCSR(t, "GW-CLOSED"),
	})
	w := callClaim(t, s, string(body))
	if w.Code != 500 {
		t.Fatalf("status %d want 500 (%s)", w.Code, w.Body.String())
	}
	if claimRedeemed(t, s, code) {
		t.Fatal("a fail-closed claim burned the one-time code")
	}
	var status string
	if err := s.st.Pool.QueryRow(ctx, `SELECT status FROM gateways WHERE id=$1`, gwID).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("gateway must stay pending: %v %q", err, status)
	}
}

func TestIntegrationClaimHalfConfiguredCAFailsClosed(t *testing.T) {
	s, _ := testServer(t)
	s.secret = []byte("claim-test-secret-claim-test-secret-42")
	ctx := context.Background()
	defer s.st.Pool.Exec(ctx, `DELETE FROM enrollment_tokens WHERE tenant_id='itest-claim4'`)
	defer s.st.Pool.Exec(ctx, `DELETE FROM gateways WHERE tenant_id='itest-claim4'`)
	defer s.st.Pool.Exec(ctx, `DELETE FROM sites WHERE tenant_id='itest-claim4'`)
	// only one of the CA pair set: misconfiguration, fail closed without burn
	t.Setenv("MTLS_CA_CERT", filepath.Join(t.TempDir(), "ca.crt"))
	t.Setenv("MTLS_CA_KEY", "")

	code, _ := seedClaim(t, s, "itest-claim4", "GW-HALF")
	body, _ := json.Marshal(map[string]string{
		"claim_code": code, "serial": "GW-HALF", "csr_pem": claimCSR(t, "GW-HALF"),
	})
	w := callClaim(t, s, string(body))
	if w.Code != 500 {
		t.Fatalf("status %d want 500 (%s)", w.Code, w.Body.String())
	}
	if claimRedeemed(t, s, code) {
		t.Fatal("a half-configured CA burned the one-time code")
	}
}
