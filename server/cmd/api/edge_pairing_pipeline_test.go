package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
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

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/brokeracl"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/pki"
	"github.com/golang-jwt/jwt/v5"
)

// Compiled edge -> HTTP challenge -> certificate-only broker -> compiled ingest
// -> PostgreSQL observation -> HTTP receipt -> certificate install. All identity,
// certs, config, broker and DB schema are disposable. No meter/source commands.
func TestIntegrationCompiledPairingProofAndACLWatcher(t *testing.T) {
	broker, edge, ingest := os.Getenv("TEST_MOSQUITTO_BINARY"), os.Getenv("TEST_EDGE_BINARY"), os.Getenv("TEST_INGEST_BINARY")
	if broker == "" || edge == "" || ingest == "" {
		t.Skip("compiled edge/ingest and disposable Mosquitto required")
	}
	addrs, _ := net.InterfaceAddrs()
	var ip net.IP
	for _, addr := range addrs {
		a, _, _ := net.ParseCIDR(addr.String())
		if a.To4() != nil && (a.IsPrivate() || a.IsLinkLocalUnicast()) && !a.IsLoopback() {
			ip = a.To4()
			break
		}
	}
	if ip == nil {
		t.Skip("sandbox private IPv4 required")
	}
	s := proofTestServer(t)
	dir := t.TempDir()
	identity := filepath.Join(dir, "identity")
	os.Mkdir(identity, 0700)
	_, err := s.st.Pool.Exec(t.Context(), `DELETE FROM edge_identity_migrations;UPDATE gateways SET cert_fingerprint=NULL WHERE id='itest-pairproof-gw'`)
	if err != nil {
		t.Fatal(err)
	}
	ca, caKey, err := pki.GenerateCA("Synthetic pairing device CA", 1)
	if err != nil {
		t.Fatal(err)
	}
	brokerCA, brokerCAKey, err := pki.GenerateCA("Synthetic pairing broker CA", 1)
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	caPath, keyPath, brokerCAPath := write("device-ca.crt", ca), write("device-ca.key", caKey), write("broker-ca.crt", brokerCA)
	leaf := func(cn string, server bool) (string, string) {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		kd, _ := x509.MarshalECPrivateKey(key)
		var cert []byte
		if server {
			cb, _ := pem.Decode(brokerCA)
			kb, _ := pem.Decode(brokerCAKey)
			parent, _ := x509.ParseCertificate(cb.Bytes)
			signer, _ := x509.ParseECPrivateKey(kb.Bytes)
			template := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn}, IPAddresses: []net.IP{ip}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
			der, e := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, signer)
			if e != nil {
				t.Fatal(e)
			}
			cert = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		} else {
			csr, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: cn}}, key)
			var e error
			cert, _, e = pki.SignCSR(ca, caKey, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}), cn, 1)
			if e != nil {
				t.Fatal(e)
			}
		}
		return write(cn+".crt", cert), write(cn+".key", pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}))
	}
	bc, bk := leaf("broker", true)
	ic, ik := leaf("ingest", false)
	savedKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	kd, _ := x509.MarshalECPrivateKey(savedKey)
	keyBefore := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd})
	secret := []byte("synthetic-pairproof-only-not-an-owner-secret")
	sign := func(user, role string) string {
		raw, e := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{TenantID: "itest-pairproof", Role: role, RegisteredClaims: jwt.RegisteredClaims{Subject: user, IssuedAt: jwt.NewNumericDate(time.Now()), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}).SignedString(secret)
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	identityBefore, _ := json.Marshal(map[string]string{"gateway_id": "itest-pairproof-gw", "ingest_token": sign("itest-pairproof-gw", "gateway")})
	configBefore := []byte("fixture: retained\n")
	os.WriteFile(filepath.Join(identity, "identity.key"), keyBefore, 0600)
	os.WriteFile(filepath.Join(identity, "identity.json"), identityBefore, 0600)
	config := write("existing-config.yaml", configBefore)
	acl := filepath.Join(dir, "acl")
	if err := brokeracl.WriteAtomic(acl, brokeracl.Generate(nil)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BROKER_ACL_FILE", acl)
	t.Setenv("MTLS_CA_CERT", caPath)
	t.Setenv("MTLS_CA_KEY", keyPath)
	t.Setenv("EDGE_MQTT_CA_FILE", brokerCAPath)
	t.Setenv("EDGE_MQTT_TLS", "true")
	t.Setenv("EDGE_MQTT_HOST", ip.String())
	listen, err := net.Listen("tcp", net.JoinHostPort(ip.String(), "0"))
	if err != nil {
		t.Fatal(err)
	}
	port := fmt.Sprint(listen.Addr().(*net.TCPAddr).Port)
	listen.Close()
	t.Setenv("EDGE_MQTT_PORT", port)
	conf := write("broker.conf", []byte(fmt.Sprintf("listener %s %s\ncafile %s\ncertfile %s\nkeyfile %s\nrequire_certificate true\nuse_identity_as_username true\nallow_anonymous false\nacl_file %s\npersistence false\n", port, ip, caPath, bc, bk, acl)))
	bin := filepath.Join(dir, "bin")
	os.Mkdir(bin, 0700)
	if err = os.Symlink(broker, filepath.Join(bin, "mosquitto")); err != nil {
		t.Fatal(err)
	}
	start := func(name, binary string, args []string, env []string) *exec.Cmd {
		f, e := os.Create(filepath.Join(dir, name+".log"))
		if e != nil {
			t.Fatal(e)
		}
		cmd := exec.Command(binary, args...)
		cmd.Env = append(os.Environ(), env...)
		cmd.Stdout = f
		cmd.Stderr = f
		if e = cmd.Start(); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			cmd.Process.Signal(syscall.SIGTERM)
			done := make(chan struct{})
			go func() { cmd.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				cmd.Process.Kill()
				<-done
			}
			f.Close()
		})
		return cmd
	}
	start("broker", "sh", []string{"../../../scripts/broker-acl-supervisor.sh", conf, acl}, []string{"PATH=" + bin + ":" + os.Getenv("PATH")})
	wait := func(label string, condition func() bool) {
		t.Helper()
		until := time.Now().Add(15 * time.Second)
		for !condition() {
			if time.Now().After(until) {
				a, _ := os.ReadFile(filepath.Join(dir, "broker.log"))
				b, _ := os.ReadFile(filepath.Join(dir, "ingest.log"))
				t.Fatalf("timeout %s broker=%s ingest=%s", label, a, b)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	wait("broker", func() bool {
		c, e := net.DialTimeout("tcp", net.JoinHostPort(ip.String(), port), 100*time.Millisecond)
		if e == nil {
			c.Close()
		}
		return e == nil
	})
	start("ingest", ingest, nil, []string{"DATABASE_URL=" + os.Getenv("TEST_DATABASE_URL"), "MQTT_TLS=true", "MQTT_HOST=" + ip.String(), "MQTT_PORT=" + port, "MQTT_CA_FILE=" + brokerCAPath, "MQTT_CERT_FILE=" + ic, "MQTT_KEY_FILE=" + ik, "BROKER_CERT_ONLY=1", "INGEST_SHARED_GROUP=", "SPARKPLUG_TENANT=", "SMTP_HOST=", "SLACK_BOT_TOKEN="})
	wait("ingest ready", func() bool {
		b, _ := os.ReadFile(filepath.Join(dir, "ingest.log"))
		return bytes.Contains(b, []byte("ingest subscriptions ready"))
	})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/edge/self/identity-migrations", s.requestIdentityMigration)
	mux.HandleFunc("GET /v1/edge/self/identity-migrations/{id}", s.getIdentityMigration)
	mux.HandleFunc("POST /v1/edge/self/identity-migrations/{id}/broker-proof", s.createPairingProof)
	mux.HandleFunc("GET /v1/edge/self/broker-proofs/{proof}", s.getPairingProof)
	// Use the production JWT/API-key, live-user, customer-scope and cache
	// middleware, not an injected role/principal fixture.
	secured := auth.Middleware(secret, s.resolveAPIKey)(s.invalidateOnWrite(s.activeUser(s.customerScope(mux))))
	var holdReceipt atomic.Bool
	holdReceipt.Store(true)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		if holdReceipt.Load() && strings.Contains(r.URL.Path, "/broker-proofs/") {
			recorder := httptest.NewRecorder()
			secured.ServeHTTP(recorder, r)
			if recorder.Code == 200 {
				var reply map[string]any
				json.Unmarshal(recorder.Body.Bytes(), &reply)
				reply["state"] = "waiting_for_broker_observation"
				reply["observed_at"] = nil
				writeJSON(w, 200, reply)
				return
			}
			w.WriteHeader(recorder.Code)
			w.Write(recorder.Body.Bytes())
			return
		}
		secured.ServeHTTP(w, r)
	}))
	defer api.Close()
	if response, e := http.Get(api.URL + "/v1/edge/self/broker-proofs/unknown"); e != nil {
		t.Fatal(e)
	} else {
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatal("unauthenticated proof endpoint accepted", response.StatusCode)
		}
	}
	// These calls pass through the same production authentication and own-
	// gateway role boundary, before any proof row can be created.
	for _, check := range []struct {
		token string
		want  int
	}{{"invalid.jwt.signature", 401}, {sign("test-user", "admin"), 403}, {sign("other-gateway", "gateway"), 404}} {
		req, _ := http.NewRequest("GET", api.URL+"/v1/edge/self/broker-proofs/unknown", nil)
		req.Header.Set("Authorization", "Bearer "+check.token)
		response, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		response.Body.Close()
		if response.StatusCode != check.want {
			t.Fatal("proof auth boundary", response.StatusCode, check.want)
		}
	}
	edgeRun := func(flag string) ([]byte, error) {
		cmd := exec.Command(edge, flag, "-setup-server", api.URL, "-setup-serial", "SER-itest-pairproof", "-identity-dir", identity, "-config", config)
		return cmd.CombinedOutput()
	}
	if out, e := edgeRun("-request-secure-pairing"); e != nil {
		t.Fatalf("request: %v %s", e, out)
	}
	raw, _ := os.ReadFile(filepath.Join(identity, "migration-request.json"))
	var request struct {
		ID  string `json:"id"`
		Key string `json:"key_sha256"`
		CSR string `json:"csr_sha256"`
	}
	if json.Unmarshal(raw, &request) != nil || request.ID == "" {
		t.Fatal("request not saved")
	}
	admin := http.NewServeMux()
	admin.HandleFunc("POST /approve/{id}", s.approveIdentityMigration)
	payload, _ := json.Marshal(map[string]any{"key_sha256": request.Key, "csr_sha256": request.CSR, "compared_local_fingerprint": true})
	r := httptest.NewRequest("POST", "/approve/"+request.ID, bytes.NewReader(payload))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Authorization", "Bearer "+sign("test-user", "admin"))
	w := httptest.NewRecorder()
	auth.Middleware(secret, s.resolveAPIKey)(s.invalidateOnWrite(s.activeUser(s.customerScope(admin)))).ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	// A TLS CONNECT or even a publish acknowledgment cannot replace the
	// independent current receipt. Withhold that receipt for one explicit
	// attempt and require timeout without any certificate/config/key replacement.
	if out, e := edgeRun("-finish-secure-pairing"); e == nil {
		t.Fatalf("installed without receipt: %s", out)
	}
	if _, e := os.Stat(filepath.Join(identity, "identity.crt")); !os.IsNotExist(e) {
		t.Fatal("missing receipt changed certificate files", e)
	}
	for name, want := range map[string][]byte{"identity.key": keyBefore, "identity.json": identityBefore} {
		got, _ := os.ReadFile(filepath.Join(identity, name))
		if !bytes.Equal(got, want) {
			t.Fatal(name, "changed before receipt")
		}
	}
	holdReceipt.Store(false)
	// No explicit Docker/broker reload. The non-root supervisor must observe
	// the new complete ACL and the compiled edge must wait for a fresh receipt.
	if out, e := edgeRun("-finish-secure-pairing"); e != nil {
		t.Fatalf("finish: %v %s", e, out)
	} else if !bytes.Contains(out, []byte("server observed a fresh own-gateway broker proof")) {
		t.Fatal(string(out))
	}
	for name, want := range map[string][]byte{"identity.key": keyBefore, "identity.json": identityBefore} {
		got, _ := os.ReadFile(filepath.Join(identity, name))
		if !bytes.Equal(got, want) {
			t.Fatal(name, "changed")
		}
	}
	got, _ := os.ReadFile(config)
	if !bytes.Equal(got, configBefore) {
		t.Fatal("config changed")
	}
	if _, e := os.Stat(filepath.Join(identity, "identity.crt")); e != nil {
		t.Fatal("certificate not installed")
	}
	var seen bool
	if e := s.st.Pool.QueryRow(t.Context(), `SELECT observed_at IS NOT NULL FROM edge_pairing_proofs WHERE gateway_id='itest-pairproof-gw'`).Scan(&seen); e != nil || !seen {
		t.Fatal("no DB authorization receipt", e)
	}
	logs, _ := os.ReadFile(filepath.Join(dir, "broker.log"))
	if !strings.Contains(string(logs), "reload signal sent") {
		t.Fatal("ACL watcher did not reload")
	}
	t.Log("PASS compiled edge, API handlers, real TLS broker ACL watcher, compiled ingest, fresh PostgreSQL own-gateway authorization receipt and unchanged saved key/identity/config. No source or hardware proof.")
}
