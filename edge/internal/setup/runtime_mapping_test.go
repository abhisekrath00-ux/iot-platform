package setup

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/claim"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRuntimeMappingRefusesUncheckedSourcesAndLocalSecrets(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic-ca"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	trust := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	base := "gateway_id: gateway\ntenant_id: tenant\nserial: serial\nmqtt: {host: broker.example.invalid, port: 8883, tls: true}\nautodetect: {enabled: false}\n"
	build := func(devices string) (RuntimeMapping, error) {
		return buildRuntimeMappingChecked([]byte(base+devices), &claim.Identity{GatewayID: "gateway"}, "serial", "tenant", "/etc/review/identity", "/etc/review/inputs/broker-ca.pem", trust, "/opt/review/edge-agent", "/etc/review/runtime.yaml", "/var/lib/review-runtime", "hexreview")
	}
	for _, tc := range []struct{ name, devices string }{
		{"missing-device-id", "devices: [{profile: modbus-tcp}]\n"},
		{"duplicate-device-id", "devices: [{id: repeated}, {id: repeated}]\n"},
		{"missing-point-id", "devices: [{id: meter, points: [{register: 1}]}]\n"},
		{"duplicate-point-id", "devices: [{id: meter, points: [{id: value}, {id: value}]}]\n"},
		{"invalid-serial-address", "devices: [{id: meter, port: /dev/ttyS0, address: 999}]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, e := build(tc.devices)
			if e == nil || m != (RuntimeMapping{}) {
				t.Fatal("invalid source returned review mapping", m, e)
			}
		})
	}
	for _, field := range []string{"client_cert", "client_key", "server_cert", "password_env", "community_env", "snmp_auth_pass_env", "snmp_priv_pass_env"} {
		t.Run(field, func(t *testing.T) {
			m, e := build("devices: [{id: meter, " + field + ": local-secret-selection}]\n")
			if e == nil || m != (RuntimeMapping{}) {
				t.Fatal("local credential selection accepted", m, e)
			}
		})
	}
	m, err := build("devices: [{id: meter, profile: modbus-tcp, host: 192.0.2.1, interval: 100ms, points: [{id: power, register: 1, min: 0, max: 1000}]}]\n")
	if err != nil || m.CanStart || !strings.Contains(m.ConfigYAML, "power") || strings.Contains(m.Unit, "[Install]") {
		t.Fatal("explicit read-only source mapping", m, err)
	}
}

func TestRuntimeMappingCommandRefusesSourceCredentialSelectionWithoutOutput(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("private identity store command is Linux-only; other platforms remain fail closed")
	}
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	identity := filepath.Join(root, "identity")
	inputs := filepath.Join(root, "inputs")
	for _, p := range []string{identity, inputs} {
		if e := os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	caKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic-ca"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, e := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if e != nil {
		t.Fatal(e)
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	keyDER, e := x509.MarshalECPrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "serial"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	certDER, e := x509.CreateCertificate(rand.Reader, cert, ca, &key.PublicKey, caKey)
	if e != nil {
		t.Fatal(e)
	}
	trust := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	save := func(dir, name string, b []byte) {
		t.Helper()
		if e := os.WriteFile(filepath.Join(dir, name), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	save(identity, "identity.key", pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	save(identity, "identity.crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}))
	save(identity, "device-ca.pem", trust)
	save(identity, "identity.json", []byte(`{"gateway_id":"gateway","ingest_token":"synthetic-no-real-token"}`))
	save(inputs, "broker-ca.pem", trust)
	if _, e := claim.ReadCandidatePrivateFiles(identity, "identity.json", "identity.key", "identity.crt", "device-ca.pem"); e != nil {
		t.Fatal("fixture identity", e)
	}
	request := map[string]string{"IdentityDir": identity, "InputDir": inputs, "Serial": "serial", "Tenant": "tenant", "Binary": "/opt/review/edge-agent", "ConfigPath": "/etc/review/runtime.yaml", "StateDir": "/var/lib/review-runtime", "User": "hexreview"}
	raw, e := json.Marshal(request)
	if e != nil {
		t.Fatal(e)
	}
	base := "gateway_id: gateway\ntenant_id: tenant\nserial: serial\nmqtt: {host: broker.example.invalid, port: 8883, tls: true}\nautodetect: {enabled: false}\n"
	for _, devices := range []string{"devices: [{id: meter, client_key: /etc/other/key}]\n", "devices: [{id: repeated}, {id: repeated}]\n"} {
		save(inputs, "server-config.yaml", []byte(base+devices))
		var out bytes.Buffer
		if e := RunRuntimeMapping(bytes.NewReader(raw), &out); e == nil || out.Len() != 0 {
			t.Fatal("unsafe command emitted mapping", e, out.String())
		}
	}
	save(inputs, "server-config.yaml", []byte(base+"devices: []\n"))
	var out bytes.Buffer
	if e := RunRuntimeMapping(bytes.NewReader(raw), &out); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(out.String(), "synthetic-no-real-token") || strings.Contains(out.String(), "PRIVATE KEY") {
		t.Fatal("mapping disclosed identity material")
	}
	var result RuntimeMapping
	if e := json.Unmarshal(out.Bytes(), &result); e != nil || result.CanStart || result.ConfigYAML == "" {
		t.Fatal("bad review output", e)
	}
}
