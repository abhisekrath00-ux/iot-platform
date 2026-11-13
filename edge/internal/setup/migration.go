package setup

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MigrationCSR is public material only. Creating it changes no identity/config,
// contacts no server and grants no permission to issue or install a certificate.
// The user compares KeySHA256 locally with the administrator's pending request.
type MigrationCSR struct {
	CSRPEM    string `json:"csr_pem"`
	KeySHA256 string `json:"key_sha256"`
	CSRSHA256 string `json:"csr_sha256"`
}

// ExistingKeyCSR reuses the saved key. There is deliberately no key generation
// fallback: a missing or unsupported key requires recovery, not enrollment.
func ExistingKeyCSR(dir, serial string) (MigrationCSR, error) {
	var out MigrationCSR
	if serial == "" || len(serial) > 128 || strings.TrimSpace(serial) != serial || strings.ContainsAny(serial, "\r\n\x00") {
		return out, fmt.Errorf("saved gateway serial is required")
	}
	b, err := os.ReadFile(filepath.Join(dir, "identity.key"))
	if err != nil {
		return out, fmt.Errorf("saved private key unavailable; no replacement was created")
	}
	block, rest := pem.Decode(b)
	if block == nil || len(strings.TrimSpace(string(rest))) != 0 {
		return out, fmt.Errorf("saved private key is not one PEM key")
	}
	var key any
	switch block.Type {
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(block.Bytes)
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	default:
		return out, fmt.Errorf("saved private key type is unsupported")
	}
	if err != nil {
		return out, fmt.Errorf("saved private key is invalid")
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return out, fmt.Errorf("saved key cannot sign")
	}
	switch k := key.(type) {
	case *ecdsa.PrivateKey:
		if k.Curve.Params().BitSize < 256 {
			return out, fmt.Errorf("saved EC key is too small")
		}
	case *rsa.PrivateKey:
		if k.N.BitLen() < 2048 {
			return out, fmt.Errorf("saved RSA key is too small")
		}
	default:
		return out, fmt.Errorf("saved key algorithm is unsupported for migration")
	}
	public, err := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil {
		return out, fmt.Errorf("saved public key unavailable")
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: serial}}, signer)
	if err != nil {
		return out, fmt.Errorf("saved key could not sign migration request")
	}
	keyHash, csrHash := sha256.Sum256(public), sha256.Sum256(der)
	out.KeySHA256 = hex.EncodeToString(keyHash[:])
	out.CSRSHA256 = hex.EncodeToString(csrHash[:])
	out.CSRPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
	return out, nil
}
