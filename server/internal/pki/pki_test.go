package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"testing"
	"time"
)

func TestCAAndSign(t *testing.T) {
	caPEM, keyPEM, err := GenerateCA("Hexmon Device CA", 10)
	if err != nil {
		t.Fatal(err)
	}

	// gateway-side key + CSR
	gwKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	csrDER, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "AXON-0001"},
	}, gwKey)
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	certPEM, fp, err := SignCSR(caPEM, keyPEM, csrPEM, "AXON-0001", 825)
	if err != nil {
		t.Fatal(err)
	}
	if len(fp) != 64 {
		t.Fatalf("fingerprint %q", fp)
	}
	block, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if cert.Subject.CommonName != "AXON-0001" {
		t.Fatalf("CN %q", cert.Subject.CommonName)
	}
	if time.Until(cert.NotAfter) > 826*24*time.Hour {
		t.Fatal("ttl exceeded")
	}

	// CN mismatch must be rejected (prevents claiming another serial's cert)
	if _, _, err := SignCSR(caPEM, keyPEM, csrPEM, "AXON-9999", 825); err == nil {
		t.Fatal("CN mismatch accepted")
	}
	// tampered CSR must fail signature check
	bad := append([]byte(nil), csrDER...)
	bad[len(bad)-10] ^= 0xFF
	badPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: bad})
	if _, _, err := SignCSR(caPEM, keyPEM, badPEM, "AXON-0001", 825); err == nil {
		t.Fatal("tampered CSR accepted")
	}
}
