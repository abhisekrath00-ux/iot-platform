package setup

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/claim"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateIdentityRequiresMatchingTrustedCurrentPair(t *testing.T) {
	dir := t.TempDir()
	if ValidateIdentity(dir, "serial") == nil {
		t.Fatal("empty identity accepted")
	}
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-ca"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "ca.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0600)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalECPrivateKey(key)
	os.WriteFile(filepath.Join(dir, "identity.key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0600)
	cert := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "serial"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	save := func() {
		d, e := x509.CreateCertificate(rand.Reader, cert, ca, &key.PublicKey, caKey)
		if e != nil {
			t.Fatal(e)
		}
		os.WriteFile(filepath.Join(dir, "identity.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d}), 0600)
	}
	save()
	if err := ValidateIdentity(dir, "serial"); err != nil {
		t.Fatal(err)
	}
	// New accepted identities have issuing CA only, distinct from broker CA.
	if err = os.Rename(filepath.Join(dir, "ca.pem"), filepath.Join(dir, "device-ca.pem")); err != nil {
		t.Fatal(err)
	}
	if err = ValidateIdentity(dir, "serial"); err != nil {
		t.Fatal("device-only issuing trust refused", err)
	}
	serverConfig := []byte("gateway_id: gateway\ntenant_id: tenant\nserial: serial\nmqtt: {host: broker.example.invalid, port: 8883, tls: true}\nautodetect: {enabled: false}\ndevices: []\n")
	brokerPath := filepath.Join(t.TempDir(), "broker-transport.pem")
	if err = os.WriteFile(brokerPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0600); err != nil {
		t.Fatal(err)
	}
	mapping, err := BuildRuntimeMapping(serverConfig, &claim.Identity{GatewayID: "gateway"}, "serial", "tenant", dir, brokerPath, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})), "/opt/hexthings/edge-agent", "/etc/hexthings/runtime.yaml", "/var/lib/hexthings-runtime", "hexmon")
	if err != nil || mapping.CanStart || !strings.Contains(mapping.Unit, "-identity-dir "+dir) || strings.Contains(mapping.Unit, "[Install]") || !strings.Contains(mapping.ConfigYAML, filepath.Join(dir, "identity.key")) {
		t.Fatal("runtime mapping", err, mapping)
	}
	if _, err = BuildRuntimeMapping(serverConfig, &claim.Identity{GatewayID: "gateway"}, "serial", "tenant", dir, brokerPath, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})), "/opt/hexthings/edge%agent", "/etc/hexthings/runtime.yaml", "/var/lib/hexthings-runtime", "root"); err == nil {
		t.Fatal("unsafe service mapping accepted")
	}
	if ValidateIdentity(dir, "wrong") == nil {
		t.Fatal("wrong serial accepted")
	}
	cert.NotAfter = time.Now().Add(-time.Second)
	save()
	if ValidateIdentity(dir, "serial") == nil {
		t.Fatal("expired accepted")
	}
}
