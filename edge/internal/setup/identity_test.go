package setup

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidateIdentityRequiresMatchingTrustedCurrentPair(t *testing.T) {
	dir := t.TempDir()
	if ValidateIdentity(dir, "serial") == nil {
		t.Fatal("empty identity accepted")
	}
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-ca"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "ca.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0600)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalECPrivateKey(key)
	os.WriteFile(filepath.Join(dir, "identity.key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0600)
	cert := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "serial"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	save := func() {
		d, e := x509.CreateCertificate(rand.Reader, cert, ca, &key.PublicKey, caKey)
		if e != nil {
			t.Fatal(e)
		}
		os.WriteFile(filepath.Join(dir, "identity.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d}), 0600)
	}
	save()
	if err := ValidateIdentity(dir, "serial"); err != nil {
		t.Fatal(err)
	}
	if ValidateIdentity(dir, "wrong") == nil {
		t.Fatal("wrong serial accepted")
	}
	cert.NotAfter = time.Now().Add(-time.Second)
	save()
	if ValidateIdentity(dir, "serial") == nil {
		t.Fatal("expired accepted")
	}
}
