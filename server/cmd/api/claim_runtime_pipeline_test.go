package main

// This is a same-user disposable process proof, not installation or systemd.
import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/brokeracl"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/pki"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func acceptedRuntimeBrokerSourceProof(t *testing.T, s *server, agent, identity, gateway, tenant, serial string, deviceCA, deviceCAKey []byte, mapProduct func([]byte) ([]byte, error), exactReceipt func(string, string)) {
	t.Helper()
	broker := os.Getenv("HEX_ACCEPTED_RUNTIME_MOSQUITTO")
	ingest := os.Getenv("HEX_ACCEPTED_RUNTIME_INGEST")
	if broker == "" {
		return
	}
	// A foreign preexisting telemetry event is not owned by the runtime fixture.
	sentinel := "commissioning-preexisting-sentinel"
	var sentinelExists bool
	if e := s.st.Pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM telemetry WHERE event_id=$1)`, sentinel).Scan(&sentinelExists); e != nil || sentinelExists {
		t.Fatal("sentinel not fresh", e)
	}
	t.Cleanup(func() {
		var n int
		if e := s.st.Pool.QueryRow(context.Background(), `SELECT count(*)FROM telemetry WHERE event_id=$1 AND tenant_id='preexisting-sentinel' AND value=777`, sentinel).Scan(&n); e != nil || n != 1 {
			t.Error("foreign telemetry sentinel changed", e, n)
		}
		if _, e := s.st.Pool.Exec(context.Background(), `DELETE FROM telemetry WHERE event_id=$1 AND tenant_id='preexisting-sentinel'`, sentinel); e != nil {
			t.Error(e)
		}
	})
	if _, e := s.st.Pool.Exec(t.Context(), `INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,value,unit,schema_version)VALUES($1,'preexisting-sentinel','foreign-gateway','foreign-device','foreign-point',now(),777,'x',1)`, sentinel); e != nil {
		t.Fatal(e)
	}
	// Registered before any producer/process starts. Later process cleanup runs first.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, e := s.st.Pool.Exec(ctx, `DELETE FROM telemetry WHERE tenant_id=$1 AND gateway_id=$2 AND device_id='synthetic-meter' AND point_id='temperature'`, tenant, gateway); e != nil {
			t.Error("exact owned telemetry cleanup", e)
			return
		}
		var n int
		if e := s.st.Pool.QueryRow(ctx, `SELECT count(*)FROM telemetry WHERE tenant_id=$1 AND gateway_id=$2`, tenant, gateway).Scan(&n); e != nil || n != 0 {
			t.Error("telemetry absence", n, e)
		}
	})
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	before := map[string][32]byte{}
	for _, n := range []string{"identity.json", "identity.key", "identity.crt", "device-ca.pem"} {
		b, e := os.ReadFile(filepath.Join(identity, n))
		if e != nil {
			t.Fatal(e)
		}
		before[n] = sha256.Sum256(b)
	}
	t.Cleanup(func() {
		for n, w := range before {
			b, e := os.ReadFile(filepath.Join(identity, n))
			if e != nil || sha256.Sum256(b) != w {
				t.Errorf("accepted material changed %s", n)
			}
		}
	})
	addrs, e := net.InterfaceAddrs()
	if e != nil {
		t.Fatal(e)
	}
	var ip net.IP
	for _, a := range addrs {
		v, _, _ := net.ParseCIDR(a.String())
		if v.To4() != nil && (v.IsPrivate() || v.IsLinkLocalUnicast()) && !v.IsLoopback() {
			ip = v.To4()
			break
		}
	}
	if ip == nil {
		t.Fatal("private or link-local test interface required for nonloopback runtime mapping")
	}
	save := func(n string, b []byte) string {
		t.Helper()
		p := filepath.Join(root, n)
		if e := os.WriteFile(p, b, 0600); e != nil {
			t.Fatal(e)
		}
		return p
	}
	brokerCA, brokerCAKey, e := pki.GenerateCA("Synthetic distinct runtime broker CA", 1)
	if e != nil {
		t.Fatal(e)
	}
	issue := func(name string, server bool) (string, string) {
		t.Helper()
		key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		kd, e := x509.MarshalECPrivateKey(key)
		if e != nil {
			t.Fatal(e)
		}
		var cert []byte
		if server {
			cb, _ := pem.Decode(brokerCA)
			kb, _ := pem.Decode(brokerCAKey)
			ca, e := x509.ParseCertificate(cb.Bytes)
			if e != nil {
				t.Fatal(e)
			}
			signer, e := x509.ParseECPrivateKey(kb.Bytes)
			if e != nil {
				t.Fatal(e)
			}
			tpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name}, IPAddresses: []net.IP{ip}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
			der, e := x509.CreateCertificate(rand.Reader, tpl, ca, &key.PublicKey, signer)
			if e != nil {
				t.Fatal(e)
			}
			cert = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		} else {
			csr, e := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: name}}, key)
			if e != nil {
				t.Fatal(e)
			}
			cert, _, e = pki.SignCSR(deviceCA, deviceCAKey, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}), name, 1)
			if e != nil {
				t.Fatal(e)
			}
		}
		return save(name+".crt", cert), save(name+".key", pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}))
	}
	bc, bk := issue("broker", true)
	oc, okey := issue("ingest", false)
	caPath := save("device-ca.crt", deviceCA)
	l, e := net.Listen("tcp", net.JoinHostPort(ip.String(), "0"))
	if e != nil {
		t.Fatal(e)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	aclText, _, e := brokeracl.SnapshotCertificateACL(t.Context(), s.st.Pool)
	if e != nil {
		t.Fatal(e)
	}
	acl := save("acl", []byte(aclText))
	t.Setenv("BROKER_ACL_FILE", acl)
	t.Setenv("BROKER_CERT_ONLY", "1")
	if e := s.regenerateBrokerACL(t.Context()); e != nil {
		t.Fatal(e)
	}
	var brokerProcess *os.Process
	conf := save("broker.conf", []byte(fmt.Sprintf("user root\nlistener %d %s\ncafile %s\ncertfile %s\nkeyfile %s\nrequire_certificate true\nuse_identity_as_username true\nallow_anonymous false\nacl_file %s\npersistence false\n", port, ip, caPath, bc, bk, acl)))
	start := func(name, bin string, args ...string) *os.File {
		t.Helper()
		f, e := os.Create(filepath.Join(root, name+".log"))
		if e != nil {
			t.Fatal(e)
		}
		cmd := exec.Command(bin, args...)
		cmd.Stdout = f
		cmd.Stderr = f
		cmd.Env = append(os.Environ(), "HEXMON_DATA_DIR="+filepath.Join(root, "runtime"))
		if name == "ingest" {
			cmd.Env = append(cmd.Env, "DATABASE_URL="+os.Getenv("TEST_DATABASE_URL"), "MQTT_TLS=true", "MQTT_HOST="+ip.String(), fmt.Sprintf("MQTT_PORT=%d", port), "MQTT_CA_FILE="+filepath.Join(root, "broker-ca.crt"), "MQTT_CERT_FILE="+oc, "MQTT_KEY_FILE="+okey, "BROKER_CERT_ONLY=1", "INGEST_SHARED_GROUP=", "SPARKPLUG_TENANT=", "SMTP_HOST=", "SLACK_BOT_TOKEN=")
		}
		if e = cmd.Start(); e != nil {
			t.Fatal(e)
		}
		if name == "broker" {
			brokerProcess = cmd.Process
		}
		t.Cleanup(func() {
			if e := cmd.Process.Signal(os.Interrupt); e != nil && !errors.Is(e, os.ErrProcessDone) {
				t.Error("producer signal", name, e)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case e := <-done:
				if e != nil {
					t.Error("producer exit", name, e)
				}
			case <-time.After(5 * time.Second):
				if e := cmd.Process.Kill(); e != nil {
					t.Error("producer kill", name, e)
				}
				<-done
				t.Error("producer exceeded shutdown deadline", name)
			}
			if e := f.Close(); e != nil {
				t.Error("producer log close", e)
			}
			if _, e := os.Stat(fmt.Sprintf("/proc/%d", cmd.Process.Pid)); !os.IsNotExist(e) {
				t.Error("producer PID remains", name, e)
			}
		})
		return f
	}
	start("broker", broker, "-c", conf)
	until := time.Now().Add(5 * time.Second)
	for {
		c, e := net.DialTimeout("tcp", net.JoinHostPort(ip.String(), fmt.Sprint(port)), 100*time.Millisecond)
		if e == nil {
			c.Close()
			break
		}
		if time.Now().After(until) {
			t.Fatal("disposable broker not ready", e)
		}
		time.Sleep(30 * time.Millisecond)
	}
	if ingest != "" {
		save("broker-ca.crt", brokerCA)
		start("ingest", ingest)
		until := time.Now().Add(8 * time.Second)
		for {
			b, _ := os.ReadFile(filepath.Join(root, "ingest.log"))
			if strings.Contains(string(b), "ingest subscriptions ready") {
				break
			}
			if time.Now().After(until) {
				t.Fatal("compiled ingest not ready", string(b))
			}
			time.Sleep(30 * time.Millisecond)
		}
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(brokerCA)
	pair, e := tls.LoadX509KeyPair(oc, okey)
	if e != nil {
		t.Fatal(e)
	}
	opts := mqtt.NewClientOptions().AddBroker(fmt.Sprintf("ssl://%s:%d", ip, port)).SetClientID("accepted-runtime-observer").SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, Certificates: []tls.Certificate{pair}})
	observer := mqtt.NewClient(opts)
	tok := observer.Connect()
	if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatal("observer mTLS", tok.Error())
	}
	defer observer.Disconnect(100)
	messages := make(chan []byte, 16)
	topic := "t/" + tenant + "/g/" + gateway + "/telemetry"
	tok = observer.Subscribe(topic, 1, func(_ mqtt.Client, m mqtt.Message) {
		select {
		case messages <- append([]byte(nil), m.Payload()...):
		default:
		}
	})
	if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatal("observer subscription", tok.Error())
	}
	meter, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer meter.Close()
	readRequests := make(chan struct{}, 16)
	go func() {
		for {
			c, e := meter.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				for {
					req := make([]byte, 12)
					c.SetDeadline(time.Now().Add(5 * time.Second))
					if _, e := io.ReadFull(c, req); e != nil {
						return
					}
					if req[7] != 3 || binary.BigEndian.Uint16(req[8:10]) != 10 || binary.BigEndian.Uint16(req[10:12]) != 1 {
						return
					}
					select {
					case readRequests <- struct{}{}:
					default:
					}
					reply := []byte{req[0], req[1], 0, 0, 0, 5, req[6], 3, 2, 0, 110}
					if _, e := c.Write(reply); e != nil {
						return
					}
				}
			}()
		}
	}()
	inputs := filepath.Join(root, "inputs")
	if e = os.Mkdir(inputs, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(filepath.Join(root, "runtime"), 0700); e != nil {
		t.Fatal(e)
	}
	raw := fmt.Sprintf("gateway_id: %s\ntenant_id: %s\nserial: %s\nmqtt: {host: %s, port: %d, tls: true}\nautodetect: {enabled: false}\ndevices: [{id: synthetic-meter, profile: modbus-tcp, host: 127.0.0.1, net_port: %d, address: 1, interval: 100ms, points: [{id: temperature, register: 10, func: 3, type: u16, scale: 1, min: 0, max: 1000}]}]\n", gateway, tenant, serial, ip, port, meter.Addr().(*net.TCPAddr).Port)
	if e = os.WriteFile(filepath.Join(inputs, "server-config.yaml"), []byte(raw), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(inputs, "broker-ca.pem"), brokerCA, 0600); e != nil {
		t.Fatal(e)
	}
	configPath := filepath.Join(root, "runtime.yaml")
	request, _ := json.Marshal(map[string]string{"IdentityDir": identity, "InputDir": inputs, "Serial": serial, "Tenant": tenant, "Binary": agent, "ConfigPath": configPath, "StateDir": filepath.Join(root, "runtime"), "User": "syntheticreview"})
	output, e := mapProduct(request)
	var mapped struct {
		ConfigYAML, Unit string
		CanStart         bool
	}
	if e != nil || json.Unmarshal(output, &mapped) != nil || mapped.CanStart {
		t.Fatal("actual accepted runtime mapping", e)
	}
	if e = os.WriteFile(configPath, []byte(mapped.ConfigYAML), 0600); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(agent, "-check-config", "-config", configPath, "-identity-dir", identity)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatal("actual config lint", e, string(out))
	}
	start("edge", agent, "-config", configPath, "-identity-dir", identity)
	deadline := time.After(12 * time.Second)
	for {
		select {
		case msg := <-messages:
			var row struct {
				Event   string  `json:"event_id"`
				Tenant  string  `json:"tenant_id"`
				Quality string  `json:"quality"`
				Gateway string  `json:"gateway_id"`
				Device  string  `json:"device_id"`
				Point   string  `json:"point_id"`
				Value   float64 `json:"value"`
			}
			if json.Unmarshal(msg, &row) != nil {
				continue
			}
			r := row
			if r.Tenant == tenant && r.Quality == "measured" && r.Event != "" && r.Gateway == gateway && r.Device == "synthetic-meter" && r.Point == "temperature" && r.Value == 110 {
				select {
				case <-readRequests:
				default:
					t.Fatal("telemetry without synthetic read")
				}
				if ingest != "" {
					until := time.Now().Add(8 * time.Second)
					for {
						var n int
						e := s.st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM telemetry WHERE event_id=$1 AND tenant_id=$2 AND gateway_id=$3 AND device_id='synthetic-meter' AND point_id='temperature' AND value=110 AND quality='measured'`, r.Event, tenant, gateway).Scan(&n)
						if e != nil {
							t.Fatal(e)
						}
						if n == 1 {
							break
						}
						if time.Now().After(until) {
							log, _ := os.ReadFile(filepath.Join(root, "ingest.log"))
							t.Fatal("same accepted identity event missing database receipt", string(log))
						}
						time.Sleep(30 * time.Millisecond)
					}
					if exactReceipt != nil {
						exactReceipt(gateway, r.Event)
					}
					t.Log("PASS same accepted retained identity compiled edge -> distinct-CA mTLS ACL broker -> compiled production ingest -> exact event/tenant/gateway/device/point measured110 PostgreSQL receipt")
				}
				// Existing accepted identity proves data denial after current DB revoke and ACL reload.
				pairV, e := tls.LoadX509KeyPair(filepath.Join(identity, "identity.crt"), filepath.Join(identity, "identity.key"))
				if e != nil {
					t.Fatal(e)
				}
				connectVictim := func(id string) mqtt.Client {
					o := mqtt.NewClientOptions().AddBroker(fmt.Sprintf("ssl://%s:%d", ip, port)).SetClientID(id).SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, Certificates: []tls.Certificate{pairV}})
					c := mqtt.NewClient(o)
					tok := c.Connect()
					if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
						t.Fatal("victim connect", tok.Error())
					}
					return c
				}
				existing := connectVictim("commissioning-existing")
				defer existing.Disconnect(100)
				// Drain prior high-rate measured readings before the one-shot witness.
				for len(messages) > 0 {
					<-messages
				}
				baseline := []byte(`{"probe":"before-denial"}`)
				tok := existing.Publish(topic, 1, false, baseline)
				if !tok.WaitTimeout(3*time.Second) || tok.Error() != nil {
					t.Fatal(tok.Error())
				}
				found := false
				until := time.After(3 * time.Second)
				for !found {
					select {
					case b := <-messages:
						found = bytes.Equal(b, baseline)
					case <-until:
						t.Fatal("no baseline probe")
					}
				}
				if _, e := s.st.Pool.Exec(t.Context(), `UPDATE gateways SET status='revoked' WHERE id=$1 AND tenant_id=$2`, gateway, tenant); e != nil {
					t.Fatal(e)
				}
				if e := s.regenerateBrokerACL(t.Context()); e != nil {
					t.Fatal(e)
				}
				if e := brokerProcess.Signal(syscall.SIGHUP); e != nil {
					t.Fatal(e)
				}
				// Wait for actual broker reload evidence, not a successful signal alone.
				deadline := time.Now().Add(3 * time.Second)
				for {
					b, _ := os.ReadFile(filepath.Join(root, "broker.log"))
					if strings.Contains(string(b), "Reloading config") {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("reload not observed")
					}
					time.Sleep(20 * time.Millisecond)
				}
				fresh := connectVictim("commissioning-fresh")
				defer fresh.Disconnect(100)
				for _, c := range []mqtt.Client{existing, fresh} {
					c.Publish(topic, 0, false, []byte(`{"probe":"denied-after-reload"}`)).WaitTimeout(time.Second)
				}
				denial := time.After(600 * time.Millisecond)
				for {
					select {
					case b := <-messages:
						if bytes.Contains(b, []byte("denied-after-reload")) {
							t.Fatal("revoked data delivered")
						}
					case <-denial:
						goto denied
					}
				}
			denied:
				if _, e := s.st.Pool.Exec(t.Context(), `UPDATE gateways SET status='active' WHERE id=$1 AND tenant_id=$2`, gateway, tenant); e != nil {
					t.Fatal(e)
				}
				t.Log("PASS same accepted certificate existing/fresh MQTT connections still connect but publish data denied after DB revoke -> existing ACL regeneration -> observed SIGHUP reload; no disconnect or CONNECT refusal")
				t.Log("PASS actual .run accepted identity -> checked mapping -> compiled edge same-user runtime -> distinct-CA certificate-required own-topic broker -> independent measured110 receipt; no systemd/privileged installation/physical device proof")
				return
			}
		case <-deadline:
			log, _ := os.ReadFile(filepath.Join(root, "edge.log"))
			t.Fatal("accepted runtime source receipt timeout", string(log))
		}
	}
}
