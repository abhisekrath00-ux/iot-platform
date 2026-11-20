package ingestprocessing

import (
	"bytes"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"time"
)

// validatedServerTLS freezes certificate metadata before a listener exists.
// The crypto.Signer itself remains caller-owned and must stay immutable/alive.
// Admission always uses mutual TLS. The separate management server instead
// authenticates tracked interactive sessions inside its controller.
func validatedServerTLS(c AdmissionServerConfig, mutual bool) (*tls.Config, error) {
	if (mutual && (c.ClientCAs == nil || len(c.ClientCAs.Subjects()) == 0)) || len(c.Certificate.Certificate) == 0 || c.Certificate.PrivateKey == nil {
		return nil, ErrAdmissionServer
	}
	leaf, err := x509.ParseCertificate(c.Certificate.Certificate[0])
	if err != nil {
		return nil, ErrAdmissionServer
	}
	signer, ok := c.Certificate.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, ErrAdmissionServer
	}
	a, err := x509.MarshalPKIXPublicKey(leaf.PublicKey)
	if err != nil {
		return nil, ErrAdmissionServer
	}
	b, err := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil || !bytes.Equal(a, b) {
		return nil, ErrAdmissionServer
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) || leaf.IsCA {
		return nil, ErrAdmissionServer
	}
	serverUsage := false
	for _, u := range leaf.ExtKeyUsage {
		if u == x509.ExtKeyUsageServerAuth {
			serverUsage = true
		}
	}
	if !serverUsage || len(leaf.DNSNames)+len(leaf.IPAddresses) == 0 {
		return nil, ErrAdmissionServer
	}
	cert := c.Certificate
	cert.Certificate = make([][]byte, len(c.Certificate.Certificate))
	for i, der := range c.Certificate.Certificate {
		cert.Certificate[i] = append([]byte(nil), der...)
	}
	// Parse the detached bytes too: x509 leaf slices may alias their DER.
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, ErrAdmissionServer
	}
	cert.OCSPStaple = append([]byte(nil), c.Certificate.OCSPStaple...)
	cert.SignedCertificateTimestamps = make([][]byte, len(c.Certificate.SignedCertificateTimestamps))
	for i, b := range c.Certificate.SignedCertificateTimestamps {
		cert.SignedCertificateTimestamps[i] = append([]byte(nil), b...)
	}
	cert.SupportedSignatureAlgorithms = append([]tls.SignatureScheme(nil), c.Certificate.SupportedSignatureAlgorithms...)
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, NextProtos: []string{"http/1.1"}, SessionTicketsDisabled: true}
	if mutual {
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
		cfg.ClientCAs = c.ClientCAs.Clone()
	}
	return cfg, nil
}
