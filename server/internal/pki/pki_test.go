package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
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

func TestSignCSRPKCS8AndMismatchedKeyRefusal(t *testing.T) {
	ca, key, err := GenerateCA("Synthetic", 1)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(key)
	ec, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := x509.MarshalPKCS8PrivateKey(ec)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8 := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw})
	client, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "GW-1"}}, client)
	csr := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
	if _, _, err = SignCSR(ca, pkcs8, csr, "GW-1", 1); err != nil {
		t.Fatal(err)
	}
	_, wrong, _ := GenerateCA("Other", 1)
	for _, test := range []struct {
		key  []byte
		days int
	}{{wrong, 1}, {pkcs8, 0}} {
		if _, _, err = SignCSR(ca, test.key, csr, "GW-1", test.days); err == nil {
			t.Fatal("invalid issuer/lifetime accepted")
		}
	}
}

func TestSignCSRCapsLifetimeAtIssuer(t *testing.T) {
	ca, key, _ := GenerateCA("Synthetic", 1)
	client, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "GW-1"}}, client)
	cert, _, err := SignCSR(ca, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), "GW-1", 825)
	if err != nil {
		t.Fatal(err)
	}
	cb, _ := pem.Decode(ca)
	issuer, _ := x509.ParseCertificate(cb.Bytes)
	lb, _ := pem.Decode(cert)
	leaf, _ := x509.ParseCertificate(lb.Bytes)
	if !leaf.NotAfter.Equal(issuer.NotAfter) {
		t.Fatal("client lifetime was not capped at CA")
	}
}

func TestSignCSRRefusesUnusableIssuers(t *testing.T) {
	client, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "GW-1"}}, client)
	csr := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	for _, tc := range []struct {
		name       string
		isCA       bool
		usage      x509.KeyUsage
		start, end time.Time
		rsa        bool
	}{
		{"notCA", false, x509.KeyUsageCertSign, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), false},
		{"wrongUse", true, x509.KeyUsageDigitalSignature, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), false},
		{"expired", true, x509.KeyUsageCertSign, time.Now().Add(-2 * time.Hour), time.Now().Add(-time.Hour), false},
		{"future", true, x509.KeyUsageCertSign, time.Now().Add(time.Hour), time.Now().Add(2 * time.Hour), false},
		{"unsupportedRSA", true, x509.KeyUsageCertSign, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Synthetic"}, IsCA: tc.isCA, BasicConstraintsValid: true, KeyUsage: tc.usage, NotBefore: tc.start, NotAfter: tc.end}
			var key interface{} = ec
			var pub interface{} = &ec.PublicKey
			if tc.rsa {
				key = rk
				pub = &rk.PublicKey
			}
			cert, err := x509.CreateCertificate(rand.Reader, tpl, tpl, pub, key)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := x509.MarshalPKCS8PrivateKey(key)
			if _, _, err = SignCSR(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw}), csr, "GW-1", 1); err == nil {
				t.Fatal("unusable CA accepted")
			}
		})
	}
}
