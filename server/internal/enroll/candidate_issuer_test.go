package enroll

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/pki"
	"testing"
	"time"
)

func TestCandidateIssuerMissingAuthorityRefuses(t *testing.T) {
	ca, cakey, e := pki.GenerateCA("Synthetic test", 1)
	if e != nil {
		t.Fatal(e)
	}
	_, responseKey, _ := ed25519.GenerateKey(rand.Reader)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "SER"}}, key)
	b := AttemptBinding{"attempt", "tenant", "gateway", "SER", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))}
	base := CandidateIssuer{ca, cakey, make([]byte, 32), responseKey, 30 * time.Minute}
	if _, e := base.Issue(b); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*CandidateIssuer){func(i *CandidateIssuer) { i.CA = nil }, func(i *CandidateIssuer) { i.CAKey = nil }, func(i *CandidateIssuer) { i.TokenKey = nil }, func(i *CandidateIssuer) { i.ResponseKey = nil }, func(i *CandidateIssuer) { i.Lifetime = 24 * time.Hour }} {
		v := base
		change(&v)
		if _, e := v.Issue(b); e == nil {
			t.Fatal("missing authority accepted")
		}
	}
	for _, v := range []AttemptBinding{{"attempt", "", "gateway", "SER", b.CSR}, {"attempt", "tenant", "", "SER", b.CSR}, {"attempt", "tenant", "gateway", "OTHER", b.CSR}} {
		if _, e := base.Issue(v); e == nil {
			t.Fatal("bad scope/CSR accepted")
		}
	}
}
