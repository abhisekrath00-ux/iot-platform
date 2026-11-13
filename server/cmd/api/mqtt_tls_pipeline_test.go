package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/brokeracl"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/mqttconfig"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
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
	ac, ak := issue("api", false)
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
	l.Close()
	acl := filepath.Join(dir, "acl")
	if e = brokeracl.WriteAtomic(acl, brokeracl.Generate([]brokeracl.Entry{{Serial: "synthetic-gateway", TenantID: "itest-mqpipe", GatewayID: "itest-mqpipe-gw"}})); e != nil {
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
	verifyGeneratedACLReads(t, port, filepath.Join(dir, "ca.crt"), gc, gk, ac, ak)
	verifyOneShotCommandTransport(t, port, filepath.Join(dir, "ca.crt"), gc, gk, ac, ak)
	runMQTTPipeline(t, map[string]string{"MQTT_TLS": "true", "MQTT_PORT": port, "MQTT_HOST": "127.0.0.1", "MQTT_CA_FILE": filepath.Join(dir, "ca.crt"), "MQTT_CERT_FILE": ic, "MQTT_KEY_FILE": ik, "PUBLISHER_CERT": gc, "PUBLISHER_KEY": gk})
}

// Both identities use production client options and the generated ACL. An own
// scan message is a same-publisher barrier after the foreign messages, rather
// than a sleep used as proof of denial. No requests reach real equipment.
func verifyGeneratedACLReads(t *testing.T, port, ca, gc, gk, ac, ak string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		c, e := net.DialTimeout("tcp", "127.0.0.1:"+port, 100*time.Millisecond)
		if e == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(e)
		}
		time.Sleep(25 * time.Millisecond)
	}
	connect := func(id, cert, key string) mqtt.Client {
		opts, err := mqttconfig.Options(func(k string) string {
			return map[string]string{"MQTT_TLS": "true", "MQTT_HOST": "127.0.0.1", "MQTT_PORT": port, "MQTT_CA_FILE": ca, "MQTT_CERT_FILE": cert, "MQTT_KEY_FILE": key}[k]
		}, id)
		if err != nil {
			t.Fatal(err)
		}
		c := mqtt.NewClient(opts)
		tok := c.Connect()
		if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
			t.Fatal("ACL client connect", tok.Error())
		}
		t.Cleanup(func() { c.Disconnect(100) })
		return c
	}
	gateway := connect("generated-acl-gateway", gc, gk)
	api := connect("generated-acl-api", ac, ak)
	own := "t/itest-mqpipe/g/itest-mqpipe-gw/"
	var mu sync.Mutex
	var received []string
	barrier := make(chan struct{}, 1)
	tok := gateway.SubscribeMultiple(map[string]byte{own + "diag": 1, own + "scan": 1, own + "cmd": 1, "t/itest-mqother/g/itest-mqpipe-gw/diag": 1, "t/itest-mqpipe/g/wrong-gateway/diag": 1}, func(_ mqtt.Client, m mqtt.Message) {
		mu.Lock()
		received = append(received, m.Topic())
		mu.Unlock()
		if m.Topic() == own+"scan" {
			barrier <- struct{}{}
		}
	})
	if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatal("ACL subscribe", tok.Error())
	}
	for _, topic := range []string{"t/itest-mqother/g/itest-mqpipe-gw/diag", "t/itest-mqpipe/g/wrong-gateway/diag", own + "cmd", own + "diag", own + "scan"} {
		tok = api.Publish(topic, 1, false, `{"synthetic":true}`)
		if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
			t.Fatal("API CN publish", tok.Error())
		}
	}
	select {
	case <-barrier:
	case <-time.After(5 * time.Second):
		t.Fatal("API CN own scan barrier not delivered")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 || received[0] != own+"diag" || received[1] != own+"scan" {
		t.Fatalf("generated ACL delivered unexpected topics: %v", received)
	}
	t.Log("generated ACL: API certificate CN publishes diagnostic/scan requests; gateway reads own requests only after same-client foreign-topic and denied-command barrier; API cmd publish received PUBACK but cmd delivery absent")
}

// Transport-only: the gateway test subscriber does not contain an executor.
// The payload is synthetic. No live gateway or output receives it.
func verifyOneShotCommandTransport(t *testing.T, port, ca, gc, gk, ac, ak string) {
	t.Helper()
	options := func(id, cert, key string) *mqtt.ClientOptions {
		t.Helper()
		o, e := mqttconfig.Options(func(k string) string {
			return map[string]string{"MQTT_TLS": "true", "MQTT_HOST": "127.0.0.1", "MQTT_PORT": port, "MQTT_CA_FILE": ca, "MQTT_CERT_FILE": cert, "MQTT_KEY_FILE": key}[k]
		}, id)
		if e != nil {
			t.Fatal(e)
		}
		return o
	}
	gateway := mqtt.NewClient(options("command-transport-subscriber", gc, gk))
	tok := gateway.Connect()
	if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatal("subscriber connect", tok.Error())
	}
	defer gateway.Disconnect(0)
	received := make(chan string, 8)
	topic := "t/itest-mqpipe/g/itest-mqpipe-gw/"
	tok = gateway.SubscribeMultiple(map[string]byte{topic + "diag": 1, topic + "scan": 1}, func(_ mqtt.Client, m mqtt.Message) { received <- m.Topic() })
	if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatal("subscriber subscribe", tok.Error())
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := publishCommandOnce(ctx, options("command-transport-api", ac, ak), topic+"diag", []byte(`{"synthetic":"no executor"}`), 3*time.Second); err != nil {
		t.Fatal(err)
	}
	// One-shot connections differ, so this barrier is not used to prove absence
	// of delayed duplicates. The raw-socket test separately checks cancellation and socket closure.
	if err := publishCommandOnce(ctx, options("command-transport-barrier", ac, ak), topic+"scan", []byte(`{"synthetic":true}`), 3*time.Second); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for !found[topic+"diag"] || !found[topic+"scan"] {
		select {
		case got := <-received:
			found[got] = true
		case <-ctx.Done():
			t.Fatal("one-shot TLS delivery", ctx.Err())
		}
	}
	t.Log("one-shot non-retained QoS1 transport on allowed diagnostic topic through real certificate-required broker and CN ACL reached a test-only subscriber; no executor or output")
}
