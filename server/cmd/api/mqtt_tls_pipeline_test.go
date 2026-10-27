package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func pipelineTestCerts(t *testing.T, dir string) func(string, bool) (string, string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic MQTT CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "ca.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); err != nil {
		t.Fatal(err)
	}
	serial := int64(1)
	return func(name string, server bool) (string, string) {
		serial++
		k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		c := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
		if server {
			c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
			c.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
		}
		d, e := x509.CreateCertificate(rand.Reader, c, ca, &k.PublicKey, key)
		if e != nil {
			t.Fatal(e)
		}
		kb, _ := x509.MarshalECPrivateKey(k)
		cp, kp := filepath.Join(dir, name+".crt"), filepath.Join(dir, name+".key")
		if e = os.WriteFile(cp, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d}), 0644); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(kp, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0600); e != nil {
			t.Fatal(e)
		}
		return cp, kp
	}
}

// Disposable certificate-required local broker and compiled ingest. Never a deployment credential.
func TestIntegrationMQTTTLSPipeline(t *testing.T) {
	binary := os.Getenv("TEST_MOSQUITTO_BINARY")
	if binary == "" || os.Getenv("TEST_INGEST_BINARY") == "" {
		t.Skip("TEST_MOSQUITTO_BINARY and TEST_INGEST_BINARY required")
	}
	dir := t.TempDir()
	issue := pipelineTestCerts(t, dir)
	bc, bk := issue("broker", true)
	ic, ik := issue("ingest", false)
	gc, gk := issue("synthetic-gateway", false)
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
	l.Close()
	acl := filepath.Join(dir, "acl")
	if e = os.WriteFile(acl, []byte("user ingest\ntopic read t/#\nuser synthetic-gateway\ntopic write t/itest-mqpipe/g/itest-mqpipe-gw/telemetry\ntopic write t/itest-mqpipe/g/itest-mqpipe-gw/scan/result\ntopic write t/itest-mqpipe/g/itest-mqpipe-gw/cmd/ack\n"), 0600); e != nil {
		t.Fatal(e)
	}
	conf := filepath.Join(dir, "broker.conf")
	if e = os.WriteFile(conf, []byte(fmt.Sprintf("listener %s 127.0.0.1\ncafile %s\ncertfile %s\nkeyfile %s\nrequire_certificate true\nuse_identity_as_username true\nallow_anonymous false\nacl_file %s\npersistence false\n", port, filepath.Join(dir, "ca.crt"), bc, bk, acl)), 0600); e != nil {
		t.Fatal(e)
	}
	logfile, e := os.Create(filepath.Join(dir, "broker.log"))
	if e != nil {
		t.Fatal(e)
	}
	defer logfile.Close()
	cmd := exec.Command(binary, "-c", conf)
	cmd.Stdout = logfile
	cmd.Stderr = logfile
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	runMQTTPipeline(t, map[string]string{"MQTT_TLS": "true", "MQTT_PORT": port, "MQTT_HOST": "127.0.0.1", "MQTT_CA_FILE": filepath.Join(dir, "ca.crt"), "MQTT_CERT_FILE": ic, "MQTT_KEY_FILE": ik, "PUBLISHER_CERT": gc, "PUBLISHER_KEY": gk})
}
