package setup

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestExistingKeyMigrationCSRPreservesIdentityAndUsesSameKey(t *testing.T) {
	dir := t.TempDir()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalECPrivateKey(key)
	raw := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	os.WriteFile(filepath.Join(dir, "identity.key"), raw, 0600)
	identity := []byte(`{"gateway_id":"same-gateway","ingest_token":"synthetic-only"}`)
	os.WriteFile(filepath.Join(dir, "identity.json"), identity, 0600)
	a, err := ExistingKeyCSR(dir, "SAVED-001")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ExistingKeyCSR(dir, "SAVED-001")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(a.CSRPEM))
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil || csr.Subject.CommonName != "SAVED-001" {
		t.Fatal("invalid request")
	}
	public, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	gotPublic, _ := x509.MarshalPKIXPublicKey(csr.PublicKey)
	if !bytes.Equal(public, gotPublic) {
		t.Fatal("key replaced")
	}
	h := sha256.Sum256(public)
	if a.KeySHA256 != hex.EncodeToString(h[:]) || b.KeySHA256 != a.KeySHA256 {
		t.Fatal("unstable public fingerprint")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "identity.key"))
	saved, _ := os.ReadFile(filepath.Join(dir, "identity.json"))
	if !bytes.Equal(after, raw) || !bytes.Equal(saved, identity) {
		t.Fatal("identity changed")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatal("unexpected certificate/config write")
	}
}
func TestMigrationCSRDoesNotGenerateMissingKeyOrLeakInvalidInput(t *testing.T) {
	dir := t.TempDir()
	if _, err := ExistingKeyCSR(dir, "SAVED-001"); err == nil {
		t.Fatal("missing key accepted")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal("generated replacement")
	}
	os.WriteFile(filepath.Join(dir, "identity.key"), []byte("not-a-key"), 0600)
	if _, err := ExistingKeyCSR(dir, "SAVED-001"); err == nil {
		t.Fatal("invalid key accepted")
	}
	for _, serial := range []string{"", " x", "x\n", "x\x00"} {
		if _, err := ExistingKeyCSR(dir, serial); err == nil {
			t.Fatal("bad serial accepted")
		}
	}
}
