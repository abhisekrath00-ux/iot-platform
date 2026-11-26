package enroll

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

// ValidateIssued verifies exact issued material before the transaction can
// consume a claim. It never accepts trust from the issued response itself.
// Direct low-level transaction fixtures may use synthetic callbacks; the
// assembled CandidateService only calls CandidateIssuer.Issue, which invokes
// this check before returning the response/fingerprint to Execute.
func (i CandidateIssuer) ValidateIssued(b AttemptBinding, issued CandidateIssued) error {
	refused := fmt.Errorf("candidate issued identity binding refused")
	if e := i.Validate(); e != nil {
		return e
	}
	if len(issued.Response) == 0 || len(issued.Response) > 131072 || len(issued.Fingerprint) != 64 {
		return refused
	}
	var wire struct {
		Gateway     string `json:"gateway_id"`
		Token       string `json:"ingest_token"`
		Certificate string `json:"client_cert_pem"`
		CA          string `json:"ca_cert_pem"`
		Manifest    []byte `json:"manifest"`
		Signature   []byte `json:"manifest_signature"`
	}
	if candidateStrictJSON(issued.Response, &wire) != nil || wire.Gateway != b.Gateway || wire.CA != string(i.CA) {
		return refused
	}
	pub := i.ResponseKey.Public().(ed25519.PublicKey)
	if !ed25519.Verify(pub, append([]byte("hexthings-claim-result-v1\x00"), wire.Manifest...), wire.Signature) {
		return refused
	}
	var m CandidateManifest
	if candidateStrictJSON(wire.Manifest, &m) != nil {
		return refused
	}
	digest := func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
	now := time.Now()
	if m.Version != 1 || m.Attempt != b.ID || m.Tenant != b.Tenant || m.Gateway != b.Gateway || m.Serial != b.Serial || m.CSR != digest(b.CSR) || m.Certificate != digest(wire.Certificate) || m.Token != digest(wire.Token) || m.Expires <= now.Unix() || m.Expires > now.Add(i.Lifetime+time.Second).Unix() {
		return refused
	}
	certBlock, rest := pem.Decode([]byte(wire.Certificate))
	if certBlock == nil || certBlock.Type != "CERTIFICATE" || len(rest) != 0 {
		return refused
	}
	cert, e := x509.ParseCertificate(certBlock.Bytes)
	if e != nil {
		return refused
	}
	fp := sha256.Sum256(cert.Raw)
	if hex.EncodeToString(fp[:]) != issued.Fingerprint {
		return refused
	}
	csrBlock, rest := pem.Decode([]byte(b.CSR))
	if csrBlock == nil || len(rest) != 0 {
		return refused
	}
	csr, e := x509.ParseCertificateRequest(csrBlock.Bytes)
	if e != nil || csr.CheckSignature() != nil || csr.Subject.CommonName != b.Serial {
		return refused
	}
	key, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return refused
	}
	certKey, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || !key.Equal(certKey) || cert.Subject.CommonName != b.Serial || cert.IsCA || !cert.NotAfter.After(time.Unix(m.Expires, 0)) {
		return refused
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(i.CA) {
		return refused
	}
	if _, e = cert.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); e != nil {
		return refused
	}
	claims := &auth.Claims{}
	token, e := jwt.ParseWithClaims(wire.Token, claims, func(t *jwt.Token) (any, error) { return i.TokenKey, nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired(), jwt.WithTimeFunc(func() time.Time { return now }))
	if e != nil || !token.Valid || claims.Subject != b.Gateway || claims.TenantID != b.Tenant || claims.Role != "gateway" || claims.ExpiresAt == nil || claims.ExpiresAt.Unix() != m.Expires {
		return refused
	}
	return nil
}

// The protocol only emits flat JSON objects. Reject duplicate keys, aliases,
// trailing values and unknown fields instead of accepting parser last-wins.
func candidateStrictJSON(raw []byte, target any) error {
	scan := json.NewDecoder(bytes.NewReader(raw))
	t, e := scan.Token()
	if e != nil || t != json.Delim('{') {
		return fmt.Errorf("object required")
	}
	seen := map[string]bool{}
	for scan.More() {
		t, e = scan.Token()
		name, ok := t.(string)
		if e != nil || !ok || seen[name] {
			return fmt.Errorf("duplicate key")
		}
		seen[name] = true
		var value json.RawMessage
		if scan.Decode(&value) != nil {
			return fmt.Errorf("invalid field")
		}
	}
	if _, e = scan.Token(); e != nil {
		return e
	}
	// Roundtrip field names against an empty typed object ensures that Go's
	// permissive case-insensitive field matching cannot accept alias keys.
	canonical, e := json.Marshal(target)
	if e != nil {
		return e
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(canonical, &fields) != nil {
		return fmt.Errorf("typed object required")
	}
	for name := range seen {
		if _, ok := fields[name]; !ok {
			return fmt.Errorf("unknown field")
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e = d.Decode(target); e != nil {
		return e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("trailing input")
	}
	return nil
}
