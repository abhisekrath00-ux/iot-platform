// Package pki issues per-gateway client certificates from a deployment-local
// CA. The CA key never leaves the server host; gateway keys are generated on
// the gateway itself and arrive as CSRs, so private keys never cross the wire.
package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// GenerateCA creates a self-signed ECDSA P-256 CA. Run once at deployment
// setup; store the key offline-safe (0600, or a KMS/HSM in high-assurance
// environments).
func GenerateCA(commonName string, years int) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(years, 0, 0),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), nil
}

// SignCSR verifies the CSR signature, requires CN == wantCN (the gateway
// serial; this binding is what lets the broker ACL identity to hardware), and
// issues a client-auth certificate valid for at most ttlDays, capped by the CA.
func SignCSR(caCertPEM, caKeyPEM, csrPEM []byte, wantCN string, ttlDays int) (certPEM []byte, fingerprint string, err error) {
	caBlock, _ := pem.Decode(caCertPEM)
	keyBlock, _ := pem.Decode(caKeyPEM)
	csrBlock, _ := pem.Decode(csrPEM)
	if caBlock == nil || keyBlock == nil || csrBlock == nil {
		return nil, "", errors.New("bad PEM input")
	}
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		return nil, "", fmt.Errorf("ca cert: %w", err)
	}
	// OpenSSL3 emits PKCS8 PRIVATE KEY by default; GenerateCA emits SEC1.
	// Both must bind to the same CA certificate and ECDSA signing key.
	pair, err := tls.X509KeyPair(caCertPEM, caKeyPEM)
	if err != nil {
		return nil, "", fmt.Errorf("ca key does not match certificate")
	}
	caKey, ok := pair.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, "", fmt.Errorf("ECDSA device CA required")
	}
	if !caCert.IsCA || caCert.KeyUsage&x509.KeyUsageCertSign == 0 || time.Now().Before(caCert.NotBefore) || !time.Now().Before(caCert.NotAfter) {
		return nil, "", fmt.Errorf("device CA cannot sign current client certificates")
	}
	if ttlDays < 1 {
		return nil, "", fmt.Errorf("positive client lifetime required")
	}
	csr, err := x509.ParseCertificateRequest(csrBlock.Bytes)
	if err != nil {
		return nil, "", fmt.Errorf("csr: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, "", fmt.Errorf("csr signature: %w", err)
	}
	if csr.Subject.CommonName != wantCN {
		return nil, "", fmt.Errorf("csr CN %q does not match gateway serial", csr.Subject.CommonName)
	}
	notAfter := time.Now().AddDate(0, 0, ttlDays)
	if notAfter.After(caCert.NotAfter) {
		notAfter = caCert.NotAfter
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: wantCN},
		NotBefore:    time.Now().Add(-5 * time.Minute),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, caCert, csr.PublicKey, caKey)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(der)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), hex.EncodeToString(sum[:]), nil
}
