package enroll

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/pki"
	"github.com/golang-jwt/jwt/v5"
)

func TestCandidateIssuedMaterialValidation(t *testing.T) {
	ca, cak, e := pki.GenerateCA("Synthetic response verification CA", 1)
	if e != nil {
		t.Fatal(e)
	}
	_, responseKey, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	der, e := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "SER"}}, key)
	if e != nil {
		t.Fatal(e)
	}
	b := AttemptBinding{ID: strings.Repeat("a", 64), Tenant: "tenant", Gateway: "gateway", Serial: "SER", CSR: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))}
	issuer := CandidateIssuer{CA: ca, CAKey: cak, TokenKey: []byte("synthetic-hmac-material-not-owner-keys"), ResponseKey: responseKey, Lifetime: 30 * time.Minute}
	issued, e := issuer.Issue(b)
	if e != nil {
		t.Fatal(e)
	}
	if e = issuer.ValidateIssued(b, issued); e != nil {
		t.Fatal(e)
	}
	var wire map[string]json.RawMessage
	if e = json.Unmarshal(issued.Response, &wire); e != nil {
		t.Fatal(e)
	}
	outer := func(field string, value any) CandidateIssued {
		t.Helper()
		clone := map[string]json.RawMessage{}
		for n, v := range wire {
			clone[n] = v
		}
		raw, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		clone[field] = raw
		result, e := json.Marshal(clone)
		if e != nil {
			t.Fatal(e)
		}
		return CandidateIssued{Response: result, Fingerprint: issued.Fingerprint}
	}
	for name, change := range map[string]CandidateIssued{
		"fingerprint": {Response: issued.Response, Fingerprint: strings.Repeat("f", 64)},
		"gateway":     outer("gateway_id", "other"), "ca": outer("ca_cert_pem", "other"), "certificate": outer("client_cert_pem", "other"), "token": outer("ingest_token", "other"), "signature": outer("manifest_signature", []byte("wrong")), "unknown": outer("extra", "field"),
		"duplicate": {Response: append([]byte(`{"gateway_id":"other",`), issued.Response[1:]...), Fingerprint: issued.Fingerprint},
		"alias":     {Response: []byte(strings.Replace(string(issued.Response), `"gateway_id":`, `"Gateway_ID":`, 1)), Fingerprint: issued.Fingerprint},
		"trailing":  {Response: append(append([]byte(nil), issued.Response...), []byte(` {}`)...), Fingerprint: issued.Fingerprint},
	} {
		t.Run(name, func(t *testing.T) {
			if issuer.ValidateIssued(b, change) == nil {
				t.Fatal("bad issued material accepted")
			}
		})
	}
	// Correctly signed manifests must still bind every identity field and token
	// scope. These failures are not just signature corruption checks.
	var certificate, token string
	_ = json.Unmarshal(wire["client_cert_pem"], &certificate)
	_ = json.Unmarshal(wire["ingest_token"], &token)
	for name, change := range map[string]func(*AttemptBinding){"tenant": func(b *AttemptBinding) { b.Tenant = "other" }, "attempt": func(b *AttemptBinding) { b.ID = "other" }, "serial": func(b *AttemptBinding) { b.Serial = "other" }, "csr": func(b *AttemptBinding) { b.CSR += "\n" }, "gateway": func(b *AttemptBinding) { b.Gateway = "other" }} {
		t.Run("signed-"+name, func(t *testing.T) {
			other := b
			change(&other)
			raw, e := CandidateSignedResponse(responseKey, other, certificate, string(ca), token, time.Now().Add(20*time.Minute))
			if e != nil {
				t.Fatal(e)
			}
			if issuer.ValidateIssued(b, CandidateIssued{Response: raw, Fingerprint: issued.Fingerprint}) == nil {
				t.Fatal("signed mismatch accepted")
			}
		})
	}
	for _, role := range []string{"admin", "viewer"} {
		exp := time.Now().Add(20 * time.Minute)
		tok, e := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{TenantID: b.Tenant, Role: role, RegisteredClaims: jwt.RegisteredClaims{Subject: b.Gateway, ExpiresAt: jwt.NewNumericDate(exp)}}).SignedString(issuer.TokenKey)
		if e != nil {
			t.Fatal(e)
		}
		raw, e := CandidateSignedResponse(responseKey, b, certificate, string(ca), tok, exp)
		if e != nil {
			t.Fatal(e)
		}
		if issuer.ValidateIssued(b, CandidateIssued{Response: raw, Fingerprint: issued.Fingerprint}) == nil {
			t.Fatal("signed wrong-role JWT accepted")
		}
	}
	// An otherwise correctly signed response cannot substitute another leaf
	// certificate, even with its correct new fingerprint and trusted CA.
	otherKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	otherCSR, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: b.Serial}}, otherKey)
	otherCert, otherFP, e := pki.SignCSR(ca, cak, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: otherCSR}), b.Serial, 1)
	if e != nil {
		t.Fatal(e)
	}
	rawOther, e := CandidateSignedResponse(responseKey, b, string(otherCert), string(ca), token, time.Now().Add(20*time.Minute))
	if e != nil {
		t.Fatal(e)
	}
	if issuer.ValidateIssued(b, CandidateIssued{Response: rawOther, Fingerprint: otherFP}) == nil {
		t.Fatal("signed trusted certificate for another key accepted")
	}
	// A valid gateway JWT still has to agree with the signed result expiry.
	exp := time.Now().Add(20 * time.Minute)
	scoped, e := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{TenantID: b.Tenant, Role: "gateway", RegisteredClaims: jwt.RegisteredClaims{Subject: b.Gateway, ExpiresAt: jwt.NewNumericDate(exp.Add(time.Minute))}}).SignedString(issuer.TokenKey)
	if e != nil {
		t.Fatal(e)
	}
	mismatch, e := CandidateSignedResponse(responseKey, b, certificate, string(ca), scoped, exp)
	if e != nil {
		t.Fatal(e)
	}
	if issuer.ValidateIssued(b, CandidateIssued{Response: mismatch, Fingerprint: issued.Fingerprint}) == nil {
		t.Fatal("JWT/manifest expiry mismatch accepted")
	}
	t.Log("PASS issued fingerprint/certificate/key/CA/manifest/identity/JWT agreement, including correctly signed wrong bindings, refuses before consumption")
}
