package claim

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCandidateRetainedPartialStateRefusesBeforeHTTP(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(403) }))
	defer srv.Close()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	csr, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "SER"}}, key)
	der, _ := x509.MarshalECPrivateKey(key)
	rp, roots := candidateTrustDigest(pub, ca)
	intent := candidateIntent{ID: strings.Repeat("a", 64), Server: srv.URL, Serial: "SER", CSR: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr})), Tenant: "tenant", ResponsePin: rp, DeviceRoots: roots}
	rawIntent, _ := json.Marshal(intent)
	for _, kind := range []string{"bad-marker", "invalid-id", "duplicate-intent", "changed-csr", "partial-response", "bad-key"} {
		t.Run(kind, func(t *testing.T) {
			parent := t.TempDir()
			if e := os.Chmod(parent, 0700); e != nil {
				t.Fatal(e)
			}
			dir := filepath.Join(parent, "attempt")
			store, e := candidateOpenStore(dir, true)
			if e != nil {
				t.Fatal(e)
			}
			files := map[string][]byte{"candidate-intent.json": rawIntent, "identity.key": pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), "candidate-started": []byte("uncertain\n")}
			switch kind {
			case "bad-marker":
				files["candidate-started"] = []byte("done")
			case "invalid-id":
				v := intent
				v.ID = strings.Repeat("z", 64)
				files["candidate-intent.json"], _ = json.Marshal(v)
			case "duplicate-intent":
				files["candidate-intent.json"] = append([]byte(`{"ID":"wrong",`), rawIntent[1:]...)
			case "changed-csr":
				other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				otherCSR, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "SER"}}, other)
				v := intent
				v.CSR = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: otherCSR}))
				files["candidate-intent.json"], _ = json.Marshal(v)
			case "partial-response":
				files["candidate-response.json"] = []byte(`{"partial":true}`)
			case "bad-key":
				files["identity.key"] = append(files["identity.key"], []byte("trailing")...)
			}
			for name, raw := range files {
				if e = store.Write(name, raw); e != nil {
					t.Fatal(e)
				}
			}
			_ = store.Close()
			payload, _ := json.Marshal(map[string]any{"Dir": dir, "Server": srv.URL, "Serial": "SER", "Code": "synthetic", "TransportCA": string(ca), "DeviceCA": string(ca), "ExpectedTenant": "tenant", "ResponseKey": pub})
			var out bytes.Buffer
			if e = RunCandidateRecovery(t.Context(), bytes.NewReader(payload), &out); e == nil || out.Len() != 0 {
				t.Fatal("partial state accepted")
			}
			if calls.Load() != 0 {
				t.Fatal("partial state reached HTTP")
			}
			pool := x509.NewCertPool()
			pool.AppendCertsFromPEM(ca)
			review, e := ReviewCandidateAttempt(dir, srv.URL, "SER", "tenant", pool, pub, ca)
			if e == nil || review.CanStart || review.CanClearPending || review.CanInstall {
				t.Fatal("partial state got safe review success")
			}
			for name, want := range files {
				got, e := os.ReadFile(filepath.Join(dir, name))
				if e != nil || !bytes.Equal(want, got) {
					t.Fatal("refusal modified state", name, e)
				}
			}
		})
	}
}
