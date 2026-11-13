package setup

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func migrationTestCA(t *testing.T, cn string) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	c := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, c, c, &k.PublicKey, k)
	if e != nil {
		t.Fatal(e)
	}
	c, _ = x509.ParseCertificate(der)
	return c, k, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
func migrationTestLeaf(t *testing.T, ca *x509.Certificate, caKey, key *ecdsa.PrivateKey, cn string, ip net.IP, usage x509.ExtKeyUsage) []byte {
	t.Helper()
	c := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(30 * time.Minute), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
	if ip != nil {
		c.IPAddresses = []net.IP{ip}
	}
	der, e := x509.CreateCertificate(rand.Reader, c, ca, &key.PublicKey, caKey)
	if e != nil {
		t.Fatal(e)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
func TestMigrationDistinctDeviceAndBrokerCAs(t *testing.T) {
	dir := savedMigrationKey(t)
	raw, _ := os.ReadFile(filepath.Join(dir, "identity.key"))
	block, _ := pem.Decode(raw)
	key, _ := x509.ParseECPrivateKey(block.Bytes)
	ca, caKey, deviceCA := migrationTestCA(t, "Device CA")
	_, _, brokerCA := migrationTestCA(t, "Broker CA")
	cert := migrationTestLeaf(t, ca, caKey, key, "SAVED-1", nil, x509.ExtKeyUsageClientAuth)
	b := MigrationBundle{ClientCert: string(cert), DeviceCA: string(deviceCA), BrokerCA: string(brokerCA), Host: "mqtt.example.invalid", Port: 8883}
	if _, e := ValidateMigrationBundle(dir, "SAVED-1", b); e != nil {
		t.Fatal(e)
	}
	b.DeviceCA = string(brokerCA)
	if _, e := ValidateMigrationBundle(dir, "SAVED-1", b); e == nil {
		t.Fatal("broker CA incorrectly trusted as device issuer")
	}
	b.DeviceCA = string(deviceCA)
	if _, e := ValidateMigrationBundle(dir, "WRONG", b); e == nil {
		t.Fatal("wrong serial accepted")
	}
}

// Explicit opt-in integration uses only ephemeral synthetic keys and a
// certificate-required local test broker. No owner broker/files are touched.
func TestIntegrationMigrationRealTLSBrokerAndInstall(t *testing.T) {
	binary := os.Getenv("HEXTHINGS_TEST_MOSQUITTO")
	if binary == "" {
		t.Skip("set HEXTHINGS_TEST_MOSQUITTO for disposable TLS broker test")
	}
	addrs, _ := net.InterfaceAddrs()
	var ip net.IP
	for _, a := range addrs {
		v, _, _ := net.ParseCIDR(a.String())
		if v.To4() != nil && !v.IsLoopback() && (v.IsPrivate() || v.IsLinkLocalUnicast()) {
			ip = v.To4()
			break
		}
	}
	if ip == nil {
		t.Skip("no sandbox private/link-local IPv4")
	}
	ln, e := net.Listen("tcp", net.JoinHostPort(ip.String(), "0"))
	if e != nil {
		t.Fatal(e)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	dir := savedMigrationKey(t)
	keyBefore, _ := os.ReadFile(filepath.Join(dir, "identity.key"))
	identityBefore, _ := os.ReadFile(filepath.Join(dir, "identity.json"))
	block, _ := pem.Decode(keyBefore)
	clientKey, _ := x509.ParseECPrivateKey(block.Bytes)
	ca, caKey, deviceCA := migrationTestCA(t, "Synthetic device CA")
	brokerCAcert, brokerCAkey, brokerCA := migrationTestCA(t, "Synthetic broker CA")
	serverKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serverDER, _ := x509.MarshalECPrivateKey(serverKey)
	serverCert := migrationTestLeaf(t, brokerCAcert, brokerCAkey, serverKey, "test-broker", ip, x509.ExtKeyUsageServerAuth)
	clientCert := migrationTestLeaf(t, ca, caKey, clientKey, "SAVED-1", nil, x509.ExtKeyUsageClientAuth)
	files := map[string][]byte{"device.crt": deviceCA, "server.crt": serverCert, "server.key": pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: serverDER})}
	for n, b := range files {
		if e = os.WriteFile(filepath.Join(dir, n), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	conf := fmt.Sprintf("listener %d %s\nrequire_certificate true\nallow_anonymous true\nuse_identity_as_username true\ncafile %s\ncertfile %s\nkeyfile %s\npersistence false\n", port, ip, filepath.Join(dir, "device.crt"), filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key"))
	path := filepath.Join(dir, "mosquitto.conf")
	os.WriteFile(path, []byte(conf), 0600)
	var logs bytes.Buffer
	cmd := exec.Command(binary, "-c", path)
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	time.Sleep(150 * time.Millisecond)
	bundle := MigrationBundle{ClientCert: string(clientCert), DeviceCA: string(deviceCA), BrokerCA: string(brokerCA), Host: ip.String(), Port: port}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e = InstallMigration(ctx, dir, "SAVED-1", "same-gateway", bundle, func(context.Context, mqtt.Client) error { return nil }); e != nil {
		t.Fatalf("%v", e)
	}
	if e = ValidateIdentity(dir, "SAVED-1"); e != nil {
		t.Fatal(e)
	}
	keyAfter, _ := os.ReadFile(filepath.Join(dir, "identity.key"))
	identityAfter, _ := os.ReadFile(filepath.Join(dir, "identity.json"))
	if !bytes.Equal(keyBefore, keyAfter) || !bytes.Equal(identityBefore, identityAfter) || MigrationPending(dir) {
		t.Fatal("identity changed or install incomplete")
	}
	savedCA, _ := os.ReadFile(filepath.Join(dir, "ca.pem"))
	savedDeviceCA, _ := os.ReadFile(filepath.Join(dir, "device-ca.pem"))
	if !bytes.Equal(savedCA, brokerCA) || !bytes.Equal(savedDeviceCA, deviceCA) {
		t.Fatal("CA roles conflated")
	}
}
