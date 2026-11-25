package claim

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// Verification key is a separately pinned input, never accepted from response.
func verifyCandidateManifest(raw []byte, key ed25519.PublicKey, v candidateIntent, expectedTenant string) error {
	if len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("separately pinned response key required")
	}
	var wire struct {
		Gateway     string `json:"gateway_id"`
		Token       string `json:"ingest_token"`
		Certificate string `json:"client_cert_pem"`
		CA          string `json:"ca_cert_pem"`
		Manifest    []byte `json:"manifest"`
		Signature   []byte `json:"manifest_signature"`
	}
	if candidateDecodeRecovery(raw, &wire) != nil || !ed25519.Verify(key, append([]byte("hexthings-claim-result-v1\x00"), wire.Manifest...), wire.Signature) {
		return fmt.Errorf("result signature refused")
	}
	var m struct {
		Version     int    `json:"version"`
		Attempt     string `json:"attempt"`
		Serial      string `json:"serial"`
		CSR         string `json:"csr_sha256"`
		Gateway     string `json:"gateway"`
		Tenant      string `json:"tenant"`
		Certificate string `json:"certificate_sha256"`
		Token       string `json:"token_sha256"`
		Expires     int64  `json:"expires"`
	}
	if candidateDecodeRecovery(wire.Manifest, &m) != nil {
		return fmt.Errorf("manifest invalid")
	}
	digest := func(v string) string { x := sha256.Sum256([]byte(v)); return hex.EncodeToString(x[:]) }
	if m.Version != 1 || m.Attempt != v.ID || m.Serial != v.Serial || m.CSR != digest(v.CSR) || m.Gateway == "" || m.Gateway != wire.Gateway || m.Tenant == "" || m.Tenant != expectedTenant || m.Certificate != digest(wire.Certificate) || m.Token != digest(wire.Token) || m.Expires <= time.Now().Unix() || m.Expires > time.Now().Add(time.Hour).Unix() {
		return fmt.Errorf("signed result binding/lifetime refused")
	}
	return nil
}
