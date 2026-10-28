package mqttconfig

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"math/big"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mqttTestCerts(t *testing.T, dir string) func(string, bool) (string, string) {
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

// Opt in against a disposable, locally launched Mosquitto. No real credentials.
func TestLocalMTLSTransport(t *testing.T) {
	binary := os.Getenv("TEST_MOSQUITTO_BINARY")
	if binary == "" {
		t.Skip("TEST_MOSQUITTO_BINARY required for local TLS broker")
	}
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	issue := mqttTestCerts(t, dir)
	bc, bk := issue("broker", true)
	ic, ik := issue("ingest", false)
	gc, gk := issue("synthetic-gateway", false)
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	acl := filepath.Join(dir, "acl")
	os.WriteFile(acl, []byte("user ingest\ntopic read t/#\nuser synthetic-gateway\ntopic write t/allowed/g/one/telemetry\n"), 0600)
	conf := filepath.Join(dir, "broker.conf")
	os.WriteFile(conf, []byte(fmt.Sprintf("user %s\nlistener %d 127.0.0.1\ncafile %s\ncertfile %s\nkeyfile %s\nrequire_certificate true\nuse_identity_as_username true\nallow_anonymous false\nacl_file %s\npersistence false\n", u.Username, port, filepath.Join(dir, "ca.crt"), bc, bk, acl)), 0600)
	logpath := filepath.Join(dir, "broker.log")
	logfile, e := os.Create(logpath)
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
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, e := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if e == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			b, _ := os.ReadFile(logpath)
			t.Fatalf("broker not ready %s", b)
		}
		time.Sleep(20 * time.Millisecond)
	}
	config := map[string]string{"MQTT_HOST": "127.0.0.1", "MQTT_PORT": fmt.Sprint(port), "MQTT_TLS": "true", "MQTT_CA_FILE": filepath.Join(dir, "ca.crt"), "MQTT_CERT_FILE": ic, "MQTT_KEY_FILE": ik}
	opts, e := Options(env(config), "synthetic-observer")
	if e != nil {
		t.Fatal(e)
	}
	if opts.TLSConfig.InsecureSkipVerify || opts.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Fatal("unsafe TLS")
	}
	observer := mqtt.NewClient(opts)
	tok := observer.Connect()
	if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatal("trusted service connect", tok.Error())
	}
	defer observer.Disconnect(50)
	received := make(chan string, 8)
	tok = observer.Subscribe("t/#", 1, func(_ mqtt.Client, m mqtt.Message) { received <- m.Topic() })
	if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatal("subscribe", tok.Error())
	}
	config["MQTT_CERT_FILE"], config["MQTT_KEY_FILE"] = gc, gk
	opts, e = Options(env(config), "synthetic-publisher")
	if e != nil {
		t.Fatal(e)
	}
	publisher := mqtt.NewClient(opts)
	tok = publisher.Connect()
	if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatal("trusted gateway connect", tok.Error())
	}
	defer publisher.Disconnect(50)
	publish := func(topic string) {
		t.Helper()
		tok := publisher.Publish(topic, 1, false, "synthetic")
		if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
			t.Fatal("publish", tok.Error())
		}
	}
	publish("t/allowed/g/one/telemetry")
	select {
	case got := <-received:
		if got != "t/allowed/g/one/telemetry" {
			t.Fatal(got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("allowed telemetry missing")
	}
	publish("t/other/g/one/telemetry")
	select {
	case got := <-received:
		t.Fatal("foreign ACL publish delivered", got)
	case <-time.After(250 * time.Millisecond):
	}
	// TLS rejects absent client identity and wrong broker hostname; no bypass option.
	caBytes, _ := os.ReadFile(filepath.Join(dir, "ca.crt"))
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caBytes)
	for _, tc := range []struct {
		name string
		cfg  *tls.Config
	}{{"no-client", &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, {"wrong-host", &tls.Config{RootCAs: roots, Certificates: opts.TLSConfig.Certificates, ServerName: "wrong.invalid", MinVersion: tls.VersionTLS12}}, {"untrusted-ca", &tls.Config{RootCAs: x509.NewCertPool(), Certificates: opts.TLSConfig.Certificates, MinVersion: tls.VersionTLS12}}} {
		t.Run(tc.name, func(t *testing.T) {
			o := mqtt.NewClientOptions().AddBroker(fmt.Sprintf("ssl://127.0.0.1:%d", port)).SetTLSConfig(tc.cfg).SetClientID(tc.name).SetConnectTimeout(time.Second)
			c := mqtt.NewClient(o)
			tok := c.Connect()
			if !tok.WaitTimeout(3 * time.Second) {
				t.Fatal("rejection timeout")
			}
			if tok.Error() == nil {
				c.Disconnect(0)
				t.Fatal("untrusted connection accepted")
			}
		})
	}
	b, _ := os.ReadFile(logpath)
	if strings.Contains(string(b), "synthetic-secret") {
		t.Fatal("secret logging")
	}
}
