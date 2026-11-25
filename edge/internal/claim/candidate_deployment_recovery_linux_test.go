//go:build linux

package claim

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSeparateDeploymentRecoveryFreshAuthorizationAndOrdinaryRefusal(t *testing.T) {
	dir, targets := deploymentFixture(t)
	root := filepath.Dir(dir)
	prep := filepath.Join(root, "prepared")
	raw, _ := json.Marshal(map[string]string{"Dir": prep})
	var output bytes.Buffer
	if e := RunCandidatePrepareInstall(bytes.NewReader(raw), &output); e != nil {
		t.Fatal(e)
	}
	targets.Binary = filepath.Join(prep, "prepared-agent")
	targets.Config = filepath.Join(prep, "prepared-config.yaml")
	targets.Unit = filepath.Join(prep, "prepared-unit.service")
	targets.State = filepath.Join(prep, "prepared-state")
	acceptParent := filepath.Join(root, "acceptance")
	if e := os.Mkdir(acceptParent, 0700); e != nil {
		t.Fatal(e)
	}
	targets.Destination = filepath.Join(acceptParent, "accepted")
	pub, signer, _ := ed25519.GenerateKey(rand.Reader)
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic-device-ca"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	caCert, _ := x509.ParseCertificate(caDER)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	var response []byte
	calls := 0
	revoked := false
	mutate := false
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if revoked {
			w.WriteHeader(403)
			return
		}
		var req struct {
			Attempt string `json:"attempt_id"`
			Serial  string `json:"serial"`
			CSR     string `json:"csr_pem"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			t.Error("request")
		}
		if response == nil {
			block, _ := pem.Decode([]byte(req.CSR))
			csr, e := x509.ParseCertificateRequest(block.Bytes)
			if e != nil {
				t.Error(e)
				return
			}
			cert := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: req.Serial}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(30 * time.Minute), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
			der, e := x509.CreateCertificate(rand.Reader, cert, caCert, csr.PublicKey, caKey)
			if e != nil {
				t.Error(e)
				return
			}
			certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
			hash := func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
			m, _ := json.Marshal(map[string]any{"version": 1, "attempt": req.Attempt, "serial": req.Serial, "csr_sha256": hash(req.CSR), "gateway": "gw", "tenant": "tenant", "certificate_sha256": hash(certPEM), "token_sha256": hash("token"), "expires": time.Now().Add(20 * time.Minute).Unix()})
			response, _ = json.Marshal(map[string]any{"gateway_id": "gw", "ingest_token": "token", "client_cert_pem": certPEM, "manifest": m, "manifest_signature": ed25519.Sign(signer, append([]byte("hexthings-claim-result-v1\x00"), m...))})
		}
		if mutate {
			os.WriteFile(filepath.Join(targets.DeploymentRoot, "sentinel"), []byte("changed"), 0600)
		}
		w.Write(response)
	}))
	defer srv.Close()
	transport := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	in := map[string]any{"Dir": dir, "Server": srv.URL, "Serial": "SER", "Code": "synthetic", "TransportCA": string(transport), "DeviceCA": string(ca), "ExpectedTenant": "tenant", "ResponseKey": pub, "Targets": targets}
	raw, _ = json.Marshal(in)
	output.Reset()
	if e := RunCandidateInstallStage(t.Context(), bytes.NewReader(raw), &output); e != nil {
		t.Fatal("stage", e)
	}
	delete(in, "Targets")
	recoverRaw, _ := json.Marshal(in)
	acceptInput := map[string]any{}
	for k, v := range in {
		acceptInput[k] = v
	}
	acceptInput["DestinationParent"] = filepath.Dir(targets.Destination)
	acceptInput["DestinationName"] = filepath.Base(targets.Destination)
	acceptRaw, _ := json.Marshal(acceptInput)
	output.Reset()
	if e := RunCandidateAcceptPrivateIdentity(t.Context(), bytes.NewReader(acceptRaw), &output); e != nil {
		t.Fatal("accept", e)
	}
	if e := os.Mkdir(targets.DeploymentRoot, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(targets.DeploymentRoot, "sentinel"), []byte("exact-prefix"), 0600); e != nil {
		t.Fatal(e)
	}
	inspected := 0
	applied := 0
	inspect := func(v VerifiedPrivateRuntime) error {
		inspected++
		if v.Deployment == nil || v.Deployment.Root != targets.DeploymentRoot || v.StateHandle == nil {
			return os.ErrInvalid
		}
		if e := v.Validate(); e != nil {
			return e
		}
		b, e := os.ReadFile(filepath.Join(v.Deployment.Root, "sentinel"))
		if e != nil || string(b) != "exact-prefix" {
			return os.ErrInvalid
		}
		return nil
	}
	apply := func(v VerifiedPrivateRuntime) error { applied++; return v.Validate() }
	before := calls
	if e := WithVerifiedPrivateRuntime(t.Context(), recoverRaw, apply); e == nil || calls != before || applied != 0 {
		t.Fatal("ordinary assembly loosened", e, calls, applied)
	}
	if e := WithVerifiedDeploymentRecovery(t.Context(), recoverRaw, inspect, apply); e != nil || calls != before+1 || applied != 1 || inspected < 2 {
		t.Fatal("separate recovery", e, calls, applied, inspected)
	}
	before = calls
	revoked = true
	if e := WithVerifiedDeploymentRecovery(t.Context(), recoverRaw, inspect, apply); e == nil || calls != before+1 || applied != 1 {
		t.Fatal("revocation bypassed", e, calls, applied)
	}
	revoked = false
	os.WriteFile(filepath.Join(targets.DeploymentRoot, "sentinel"), []byte("foreign"), 0600)
	before = calls
	if e := WithVerifiedDeploymentRecovery(t.Context(), recoverRaw, inspect, apply); e == nil || calls != before || applied != 1 {
		t.Fatal("foreign prefix reached HTTP", e, calls, applied)
	}
	os.WriteFile(filepath.Join(targets.DeploymentRoot, "sentinel"), []byte("exact-prefix"), 0600)
	mutate = true
	before = calls
	if e := WithVerifiedDeploymentRecovery(t.Context(), recoverRaw, inspect, apply); e == nil || calls != before+1 || applied != 1 {
		t.Fatal("during-request change applied", e, calls, applied)
	}
}
