//go:build linux && hex_deployment_native_acceptance

package setup

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
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestNativeRootOriginStoppedInstaller(t *testing.T) {
	if os.Getenv("HEX_NATIVE_STOPPED_ACCEPTANCE") != "1" {
		t.Skip("explicit disposable root gate only")
	}
	if os.Geteuid() != 0 {
		t.Fatal("native gate needs disposable root, no sudo fallback")
	}
	binary := os.Getenv("HEX_NATIVE_STOPPED_AGENT")
	if !filepath.IsAbs(binary) {
		t.Fatal("exact acceptance agent required")
	}
	if _, e := exec.LookPath("runuser"); e != nil {
		t.Fatal(e)
	}
	root, e := os.MkdirTemp("/tmp", "hex-native-stopped-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(root)
	if e = os.Chmod(root, 0755); e != nil {
		t.Fatal(e)
	}
	user := fmt.Sprintf("hexstop%d", os.Getpid())
	if out, e := exec.Command("useradd", "--system", "--no-create-home", "--shell", "/usr/sbin/nologin", user).CombinedOutput(); e != nil {
		t.Fatalf("disposable useradd: %v %s", e, out)
	}
	defer func() {
		if out, e := exec.Command("userdel", user).CombinedOutput(); e != nil {
			t.Errorf("cleanup user: %v %s", e, out)
		}
	}()
	uidRaw, e := exec.Command("id", "-u", user).Output()
	if e != nil {
		t.Fatal(e)
	}
	gidRaw, e := exec.Command("id", "-g", user).Output()
	if e != nil {
		t.Fatal(e)
	}
	serviceUID, serviceGID := strings.TrimSpace(string(uidRaw)), strings.TrimSpace(string(gidRaw))
	if serviceUID == "0" || serviceGID == "0" {
		t.Fatal("distinct nonroot identity required")
	}
	finalRoot := filepath.Join(root, "payload")
	finalState := filepath.Join(root, "state")
	finalUnit := filepath.Join(root, "synthetic-only.service")
	dir := filepath.Join(root, "attempt")
	prep := filepath.Join(root, "prepared")
	accept := filepath.Join(root, "acceptance")
	inputs := filepath.Join(root, "inputs")
	for _, p := range []string{accept, inputs} {
		if e = os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	invoke := func(flag string, in any) ([]byte, error) {
		raw, _ := json.Marshal(in)
		cmd := exec.Command(binary, flag)
		cmd.Stdin = bytes.NewReader(raw)
		cmd.Env = append(os.Environ(), "HEX_NATIVE_STOPPED_ACCEPTANCE=1")
		return cmd.CombinedOutput()
	}
	must := func(flag string, in any) []byte {
		t.Helper()
		out, e := invoke(flag, in)
		if e != nil {
			t.Fatalf("%s: %v %s", flag, e, out)
		}
		return out
	}
	must("-prepare-private-install", map[string]string{"Dir": prep})
	pub, signer, _ := ed25519.GenerateKey(rand.Reader)
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic-device-ca"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	caCert, _ := x509.ParseCertificate(caDER)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	var response []byte
	var calls atomic.Int32
	var revoked atomic.Bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if revoked.Load() {
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
		w.Write(response)
	}))
	defer srv.Close()

	transport := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	targets := map[string]string{"Binary": filepath.Join(prep, "prepared-agent"), "Config": filepath.Join(prep, "prepared-config.yaml"), "Unit": filepath.Join(prep, "prepared-unit.service"), "State": filepath.Join(prep, "prepared-state"), "Destination": filepath.Join(accept, "accepted"), "DeploymentRoot": finalRoot, "DeploymentState": finalState, "DeploymentUnit": finalUnit, "ServiceUID": serviceUID, "ServiceGID": serviceGID, "Activation": "stopped-only"}
	in := map[string]any{"Dir": dir, "Server": srv.URL, "Serial": "SER", "Code": "synthetic", "TransportCA": string(transport), "DeviceCA": string(ca), "ExpectedTenant": "tenant", "ResponseKey": pub, "Targets": targets}
	must("-create-staged-install", in)
	delete(in, "Targets")
	acceptIn := map[string]any{}
	for k, v := range in {
		acceptIn[k] = v
	}
	acceptIn["DestinationParent"] = accept
	acceptIn["DestinationName"] = "accepted"
	must("-accept-private-identity", acceptIn)
	serverConfig := []byte("gateway_id: gw\ntenant_id: tenant\nserial: SER\nmqtt: {host: broker.example.invalid, port: 8883, tls: true}\nautodetect: {enabled: false}\ndevices: []\n")
	if e = os.WriteFile(filepath.Join(inputs, "server-config.yaml"), serverConfig, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(inputs, "broker-ca.pem"), ca, 0600); e != nil {
		t.Fatal(e)
	}
	must("-assemble-private-runtime", map[string]any{"Retained": in, "InputDir": inputs, "Serial": "SER", "Tenant": "tenant", "User": user})
	before := calls.Load()
	must("-native-stopped-deployment-acceptance", in)
	if calls.Load() != before+1 {
		t.Fatal("root adapter did not fresh recover", calls.Load(), before)
	}
	must("-native-stopped-deployment-acceptance", in)
	// The stopped unit lives only under /tmp, never systemd's search paths.
	unit, e := os.ReadFile(finalUnit)
	if e != nil || bytes.Contains(unit, []byte("[Install]")) || !bytes.Contains(unit, []byte("User="+serviceUID+"\nGroup="+serviceGID)) {
		t.Fatal("exact stopped unit", e)
	}
	runAs := func(args ...string) ([]byte, error) {
		cmd := exec.Command("runuser", append([]string{"-u", user, "--"}, args...)...)
		return cmd.CombinedOutput()
	}
	out, e := runAs(filepath.Join(finalRoot, "agent"), "-check-config", "-config", filepath.Join(finalRoot, "config.yaml"), "-identity-dir", filepath.Join(finalRoot, "identity"))
	if e != nil {
		t.Fatalf("service user lint %v %s", e, out)
	}
	for _, p := range []string{finalRoot, filepath.Join(finalRoot, "agent"), filepath.Join(finalRoot, "config.yaml"), filepath.Join(finalRoot, "broker-ca.pem"), filepath.Join(finalRoot, "identity"), filepath.Join(finalRoot, "identity/identity.key"), filepath.Join(finalRoot, "identity/identity.crt"), filepath.Join(finalRoot, "identity/identity.json"), filepath.Join(finalRoot, "identity/device-ca.pem")} {
		if out, e = runAs("test", "-r", p); e != nil {
			t.Fatalf("not readable %s %v %s", p, e, out)
		}
		if _, e = runAs("test", "-w", p); e == nil {
			t.Fatal("service can write frozen payload", p)
		}
	}
	if out, e = runAs("test", "-w", finalState); e != nil {
		t.Fatalf("service state not writable %v %s", e, out)
	}
	info, e := os.Stat(finalState)
	if e != nil || info.Mode().Perm() != 0700 {
		t.Fatal("state mode", e)
	}
	st := info.Sys().(*syscall.Stat_t)
	if fmt.Sprint(st.Uid) != serviceUID || fmt.Sprint(st.Gid) != serviceGID {
		t.Fatal("state not service owned")
	}
	for _, p := range []string{finalRoot, filepath.Join(finalRoot, "agent"), filepath.Join(finalRoot, "identity/identity.key")} {
		info, e := os.Stat(p)
		if e != nil {
			t.Fatal(e)
		}
		st := info.Sys().(*syscall.Stat_t)
		if st.Uid != 0 || fmt.Sprint(st.Gid) != serviceGID {
			t.Fatal("payload ownership differs", p)
		}
	}
	keyBefore, e := os.ReadFile(filepath.Join(accept, "accepted/identity.key"))
	if e != nil {
		t.Fatal(e)
	}
	keyAfter, e := os.ReadFile(filepath.Join(finalRoot, "identity/identity.key"))
	if e != nil || !bytes.Equal(keyBefore, keyAfter) {
		t.Fatal("identity changed", e)
	}
	revoked.Store(true)
	before = calls.Load()
	if out, e = invoke("-native-stopped-deployment-acceptance", in); e == nil || calls.Load() != before+1 {
		t.Fatal("revoked root recovery accepted", e, string(out))
	}
	t.Log("PASS root-origin genuine preparation/staging/signed HTTPS enrollment/private acceptance/handoff/fresh root stopped adapter, exact repeat, revoked refusal, distinct service-user lint/read-exec/no-write and writable0700state; unit only synthetic/tmp, no systemd reload/start/enable or artifacts")
}
