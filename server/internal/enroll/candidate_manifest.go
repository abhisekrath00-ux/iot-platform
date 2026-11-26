package enroll

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

type CandidateManifest struct {
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

func CandidateSignedResponse(key ed25519.PrivateKey, b AttemptBinding, certificate, ca, token string, expiry time.Time) ([]byte, error) {
	if len(key) != ed25519.PrivateKeySize || !expiry.After(time.Now()) || expiry.After(time.Now().Add(time.Hour)) {
		return nil, fmt.Errorf("invalid candidate signing authority/lifetime")
	}
	digest := func(v string) string { x := sha256.Sum256([]byte(v)); return hex.EncodeToString(x[:]) }
	// Hash the exact retained PEM representation; the protocol already binds its
	// canonical DER hash separately in the transaction.
	m := CandidateManifest{1, b.ID, b.Serial, digest(b.CSR), b.Gateway, b.Tenant, digest(certificate), digest(token), expiry.Unix()}
	raw, e := json.Marshal(m)
	if e != nil {
		return nil, e
	}
	sig := ed25519.Sign(key, append([]byte("hexthings-claim-result-v1\x00"), raw...))
	return json.Marshal(map[string]any{"gateway_id": b.Gateway, "ingest_token": token, "client_cert_pem": certificate, "ca_cert_pem": ca, "manifest": raw, "manifest_signature": sig})
}
