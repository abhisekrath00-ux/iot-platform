package claim

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"
)

func TestCandidateManifestBindings(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	v := candidateIntent{ID: "attempt", Serial: "serial", CSR: "csr"}
	hash := func(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }
	base := map[string]any{"version": 1, "attempt": v.ID, "serial": v.Serial, "csr_sha256": hash(v.CSR), "gateway": "gw", "tenant": "tenant", "certificate_sha256": hash("cert"), "token_sha256": hash("token"), "expires": time.Now().Add(30 * time.Minute).Unix()}
	wire := func(m map[string]any, outer map[string]any, k ed25519.PrivateKey) []byte {
		b, _ := json.Marshal(m)
		w := map[string]any{"gateway_id": "gw", "ingest_token": "token", "client_cert_pem": "cert", "manifest": b, "manifest_signature": ed25519.Sign(k, append([]byte("hexthings-claim-result-v1\x00"), b...))}
		for n, v := range outer {
			w[n] = v
		}
		raw, _ := json.Marshal(w)
		return raw
	}
	if e := verifyCandidateManifest(wire(base, nil, key), pub, v, "tenant"); e != nil {
		t.Fatal(e)
	}
	for name, val := range map[string]any{"version": 2, "attempt": "other", "serial": "other", "csr_sha256": "wrong", "gateway": "other", "tenant": "foreign", "certificate_sha256": "wrong", "token_sha256": "wrong", "expires": time.Now().Add(-time.Minute).Unix()} {
		t.Run(name, func(t *testing.T) {
			changed := map[string]any{}
			for k, v := range base {
				changed[k] = v
			}
			changed[name] = val
			if e := verifyCandidateManifest(wire(changed, nil, key), pub, v, "tenant"); e == nil {
				t.Fatal("binding accepted")
			}
		})
	}
	for _, name := range []string{"gateway_id", "ingest_token", "client_cert_pem"} {
		if e := verifyCandidateManifest(wire(base, map[string]any{name: "changed"}, key), pub, v, "tenant"); e == nil {
			t.Fatal("outer tamper", name)
		}
	}
	_, wrong, _ := ed25519.GenerateKey(rand.Reader)
	if e := verifyCandidateManifest(wire(base, nil, wrong), pub, v, "tenant"); e == nil {
		t.Fatal("untrusted response key")
	}
	if e := verifyCandidateManifest(wire(base, nil, key), nil, v, "tenant"); e == nil {
		t.Fatal("missing pin")
	}
}
