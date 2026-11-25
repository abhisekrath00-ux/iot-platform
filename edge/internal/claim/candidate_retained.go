package claim

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
)

func candidateLoadIntent(store *candidateStore, server, serial, tenant string, pin ed25519.PublicKey, deviceTrust []byte) (candidateIntent, *ecdsa.PrivateKey, error) {
	var v candidateIntent
	refuse := fmt.Errorf("retained identity is partial or differs; preserve files, no request")
	marker, e := store.Read("candidate-started")
	if e != nil || (string(marker) != "uncertain\n" && string(marker) != "install-uncertain\n") {
		return v, nil, refuse
	}
	raw, e := store.Read("candidate-intent.json")
	if e != nil || candidateDecodeRecovery(raw, &v) != nil {
		return v, nil, refuse
	}
	rp, roots := candidateTrustDigest(pin, deviceTrust)
	if len(v.ID) != 64 || v.Server != server || v.Serial != serial || v.Tenant != tenant || v.ResponsePin != rp || v.DeviceRoots != roots {
		return v, nil, refuse
	}
	if _, e = hex.DecodeString(v.ID); e != nil {
		return v, nil, refuse
	}
	raw, e = store.Read("identity.key")
	if e != nil {
		return v, nil, refuse
	}
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "EC PRIVATE KEY" || len(rest) != 0 {
		return v, nil, refuse
	}
	key, e := x509.ParseECPrivateKey(block.Bytes)
	if e != nil || key.Curve.Params().BitSize != 256 {
		return v, nil, refuse
	}
	block, rest = pem.Decode([]byte(v.CSR))
	if block == nil || block.Type != "CERTIFICATE REQUEST" || len(rest) != 0 {
		return v, nil, refuse
	}
	csr, e := x509.ParseCertificateRequest(block.Bytes)
	if e != nil || csr.CheckSignature() != nil || csr.Subject.CommonName != serial {
		return v, nil, refuse
	}
	pub, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok || !pub.Equal(&key.PublicKey) {
		return v, nil, refuse
	}
	return v, key, nil
}

func candidateVerifyResponse(raw []byte, v candidateIntent, key *ecdsa.PrivateKey, roots *x509.CertPool, pin ed25519.PublicKey, tenant string) error {
	if roots == nil {
		return fmt.Errorf("separate device trust required")
	}
	if e := verifyCandidateManifest(raw, pin, v, tenant); e != nil {
		return e
	}
	var out Identity
	if json.Unmarshal(raw, &out) != nil || out.GatewayID == "" || out.IngestToken == "" {
		return fmt.Errorf("identity response invalid")
	}
	block, rest := pem.Decode([]byte(out.ClientCertPEM))
	if block == nil || block.Type != "CERTIFICATE" || len(rest) != 0 {
		return fmt.Errorf("exact certificate required")
	}
	cert, e := x509.ParseCertificate(block.Bytes)
	if e != nil {
		return e
	}
	if _, e = cert.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); e != nil {
		return fmt.Errorf("device trust refused")
	}
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || !pub.Equal(&key.PublicKey) || cert.Subject.CommonName != v.Serial {
		return fmt.Errorf("certificate key/serial mismatch")
	}
	return nil
}
