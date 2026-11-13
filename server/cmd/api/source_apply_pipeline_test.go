package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/brokeracl"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/mqttconfig"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// Disposable loopback broker, compiled ingest and edge, real Postgres, synthetic
// read-only Modbus TCP device. Restart is supervised here, not a Windows SCM proof.
func TestIntegrationCompiledSourceApplyAndReading(t *testing.T) {
	brokerBin, edgeBin, ingestBin := os.Getenv("TEST_MOSQUITTO_BINARY"), os.Getenv("TEST_EDGE_BINARY"), os.Getenv("TEST_INGEST_BINARY")
	if brokerBin == "" || edgeBin == "" || ingestBin == "" {
		t.Skip("compiled edge/ingest and disposable broker binaries required")
	}
	s, _ := testServer(t)
	tenant := "itest-source-pipeline"
	seed(t, s, tenant)
	dir := t.TempDir()
	listen, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listen.Addr().(*net.TCPAddr).Port
	listen.Close()
	brokerConfig := filepath.Join(dir, "broker.conf")
	os.WriteFile(brokerConfig, []byte(fmt.Sprintf("listener %d 127.0.0.1\nallow_anonymous true\npersistence false\n", port)), 0600)
	secure := os.Getenv("TEST_SOURCE_MTLS") == "1"
	ca, ic, ik, ac, ak, gc, gk := "", "", "", "", "", "", ""
	if secure {
		issue := pipelineTestCerts(t, dir)
		bc, bk := issue("broker", true)
		ic, ik = issue("ingest", false)
		ac, ak = issue("api", false)
		gc, gk = issue("SER-"+tenant, false)
		ca = filepath.Join(dir, "ca.crt")
		acl := filepath.Join(dir, "acl")
		if e := brokeracl.WriteAtomic(acl, brokeracl.Generate([]brokeracl.Entry{{Serial: "SER-" + tenant, TenantID: tenant, GatewayID: tenant + "-gw"}})); e != nil {
			t.Fatal(e)
		}
		os.WriteFile(brokerConfig, []byte(fmt.Sprintf("listener %d 127.0.0.1\ncafile %s\ncertfile %s\nkeyfile %s\nrequire_certificate true\nuse_identity_as_username true\nallow_anonymous false\nacl_file %s\npersistence false\n", port, ca, bc, bk, acl)), 0600)
	}

	start := func(label, binary string, args []string, env []string) (*exec.Cmd, string) {
		path := filepath.Join(dir, label+".log")
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(binary, args...)
		cmd.Env = append(os.Environ(), env...)
		cmd.Stdout = f
		cmd.Stderr = f
		if err := cmd.Start(); err != nil {
			f.Close()
			t.Fatal(err)
		}
		t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait(); f.Close() })
		return cmd, path
	}
	_, brokerLog := start("broker", brokerBin, []string{"-c", brokerConfig}, nil)
	wait := func(label string, condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			if condition() {
				return
			}
			if time.Now().After(deadline) {
				b, _ := os.ReadFile(brokerLog)
				t.Fatalf("timeout %s broker=%s", label, b)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	wait("broker", func() bool {
		c, e := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if e == nil {
			c.Close()
		}
		return e == nil
	})
	_, ingestLog := start("ingest", ingestBin, nil, append([]string{"DATABASE_URL=" + os.Getenv("TEST_DATABASE_URL"), "MQTT_HOST=127.0.0.1", fmt.Sprintf("MQTT_PORT=%d", port), "MQTT_TLS=false", "MQTT_CA_FILE=", "MQTT_CERT_FILE=", "MQTT_KEY_FILE=", "INGEST_SHARED_GROUP=", "SPARKPLUG_TENANT=", "SMTP_HOST=", "SLACK_BOT_TOKEN="}, func() []string {
		if secure {
			return []string{"MQTT_TLS=true", "MQTT_CA_FILE=" + ca, "MQTT_CERT_FILE=" + ic, "MQTT_KEY_FILE=" + ik}
		}
		return nil
	}()...))
	wait("ingest subscriptions", func() bool {
		b, _ := os.ReadFile(ingestLog)
		return strings.Contains(string(b), "ingest subscriptions ready")
	})
	opts := mqtt.NewClientOptions().AddBroker(fmt.Sprintf("tcp://127.0.0.1:%d", port)).SetClientID("source-pipeline-api")
	if secure {
		var e error
		opts, e = mqttconfig.Options(func(k string) string {
			return map[string]string{"MQTT_HOST": "127.0.0.1", "MQTT_PORT": fmt.Sprint(port), "MQTT_TLS": "true", "MQTT_CA_FILE": ca, "MQTT_CERT_FILE": ac, "MQTT_KEY_FILE": ak}[k]
		}, "source-pipeline-api")
		if e != nil {
			t.Fatal(e)
		}
	}
	publisher := mqtt.NewClient(opts)
	tok := publisher.Connect()
	if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatal("publisher", tok.Error())
	}
	defer publisher.Disconnect(100)
	s.opsPublishHook = func(topic string, b []byte) error {
		tok := publisher.Publish(topic, 1, false, b)
		if !tok.WaitTimeout(5 * time.Second) {
			return fmt.Errorf("publish timeout")
		}
		return tok.Error()
	}
	seedKey := bytes.Repeat([]byte{19}, 32)
	t.Setenv("FLEET_SIGNING_KEY", base64.StdEncoding.EncodeToString(seedKey))
	public := base64.StdEncoding.EncodeToString(ed25519.NewKeyFromSeed(seedKey).Public().(ed25519.PublicKey))
	// A synthetic Modbus TCP meter, value 110 at register10, function03 only.
	simulator, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { simulator.Close() })
	go func() {
		for {
			c, e := simulator.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				for {
					req := make([]byte, 12)
					c.SetDeadline(time.Now().Add(10 * time.Second))
					if _, e := io.ReadFull(c, req); e != nil {
						return
					}
					if req[7] != 3 {
						return
					}
					cnt := binary.BigEndian.Uint16(req[10:])
					if cnt < 1 || cnt > 32 {
						return
					}
					data := []byte{}
					for i := uint16(0); i < cnt; i++ {
						data = binary.BigEndian.AppendUint16(data, 110+i)
					}
					header := []byte{req[0], req[1], 0, 0, 0, byte(3 + len(data)), req[6], 3, byte(len(data))}
					if _, e := c.Write(append(header, data...)); e != nil {
						return
					}
				}
			}()
		}
	}()
	profile := tenant + "-profile"
	_, err = s.st.Pool.Exec(t.Context(), `INSERT INTO device_profiles(id,tenant_id,name,driver_profile,points,created_by) VALUES($1,$2,'Synthetic meter','modbus-tcp',$3,'test-user')`, profile, tenant, `[{"id":"temp","register":10,"func":3,"type":"u16","scale":1,"unit":"C","min":0,"max":1000}]`)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := json.Marshal(map[string]any{"device_profile_id": profile, "connection": map[string]any{"host": "127.0.0.1", "net_port": simulator.Addr().(*net.TCPAddr).Port, "address": 1, "interval_seconds": 1}})
	_, err = s.st.Pool.Exec(t.Context(), `UPDATE devices SET config=$1 WHERE id=$2`, cfg, tenant+"-dev")
	if err != nil {
		t.Fatal(err)
	}
	s.st.Pool.Exec(t.Context(), `DELETE FROM telemetry WHERE tenant_id=$1`, tenant)
	edgeData := filepath.Join(dir, "edge-data")
	os.MkdirAll(edgeData, 0700)
	os.WriteFile(filepath.Join(edgeData, "identity.json"), []byte(`{"gateway_id":"`+tenant+`-gw","ingest_token":"synthetic-not-a-secret"}`), 0600)
	agentConfig := filepath.Join(dir, "edge.yaml")
	mqttLine := fmt.Sprintf("{host: 127.0.0.1,port: %d,tls: false}", port)
	if secure {
		for name, path := range map[string]string{"identity.key": gk, "identity.crt": gc, "ca.pem": ca} {
			b, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			os.WriteFile(filepath.Join(edgeData, name), b, 0600)
		}
		mqttLine = fmt.Sprintf("{host: 127.0.0.1,port: %d,tls: true}", port)
	}
	os.WriteFile(agentConfig, []byte(fmt.Sprintf("gateway_id: %s-gw\ntenant_id: %s\nserial: SER-%s\nmqtt: %s\nqueue_path: %s\nui: {listen: off}\nautodetect: {enabled: false}\nmanaged_devices: true\nfleet_public_key: %s\ndevices: []\n", tenant, tenant, tenant, mqttLine, filepath.Join(edgeData, "queue.db"), public)), 0600)
	_, edgeLog := start("edge-first", edgeBin, []string{"-config", agentConfig, "-identity-dir", edgeData}, []string{"HEXMON_DATA_DIR=" + edgeData})
	wait("edge config subscription", func() bool {
		b, _ := os.ReadFile(edgeLog)
		return strings.Contains(string(b), "config push subscription ready")
	})
	m := http.NewServeMux()
	m.HandleFunc("POST /v1/gateways/{id}/config-push", s.pushGatewayConfig)
	m.HandleFunc("GET /v1/gateways/{id}/config-push", s.listConfigPushes)
	m.HandleFunc("GET /v1/gateways/{id}/source-readiness", s.sourceReadiness)
	base := "/v1/gateways/" + tenant + "-gw"
	w := call(m, tenant, "admin", "GET", base+"/source-readiness", "")
	if w.Code != 200 {
		t.Fatalf("readiness %d %s", w.Code, w.Body)
	}
	var ready struct {
		SHA string `json:"desired_sha256"`
	}
	json.Unmarshal(w.Body.Bytes(), &ready)
	bad := call(m, tenant, "admin", "POST", base+"/config-push", `{"expected_sha256":"stale"}`)
	if bad.Code != 409 {
		t.Fatal("stale review accepted", bad.Code)
	}
	w = call(m, tenant, "admin", "POST", base+"/config-push", `{"expected_sha256":"`+ready.SHA+`"}`)
	if w.Code != 202 {
		t.Fatalf("push %d %s", w.Code, w.Body)
	}
	var pushed struct {
		ID string `json:"push_id"`
	}
	json.Unmarshal(w.Body.Bytes(), &pushed)
	state := func() string {
		var status string
		s.st.Pool.QueryRow(t.Context(), `SELECT status FROM gateway_config_pushes WHERE id=$1`, pushed.ID).Scan(&status)
		return status
	}
	wait("ingested application result", func() bool { return state() == "applied" || state() == "confirmed" })
	wait("confirmed from running edge", func() bool { return state() == "confirmed" })
	wait("synthetic source reading", func() bool {
		var n int
		s.st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM telemetry WHERE tenant_id=$1 AND device_id=$2 AND point_id='temp' AND value=110`, tenant, tenant+"-dev").Scan(&n)
		return n > 0
	})
	w = call(m, tenant, "admin", "GET", base+"/config-push", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"confirmed"`) || !strings.Contains(w.Body.String(), ready.SHA) {
		t.Fatalf("scoped status %d %s", w.Code, w.Body)
	}
	foreign := call(m, "foreign-source-pipeline", "admin", "GET", base+"/config-push", "")
	if strings.TrimSpace(foreign.Body.String()) != "[]" {
		t.Fatal("status scope leak")
	}
	b, _ := os.ReadFile(filepath.Join(edgeData, "identity.json"))
	if !strings.Contains(string(b), "synthetic-not-a-secret") {
		t.Fatal("identity overwritten")
	}
	b, _ = os.ReadFile(edgeLog)
	t.Logf("authenticated_mtls=%t actual compiled edge->MQTT->ingest->Postgres confirmed and110 reading; live poll-supervisor reload without service restart; no Windows SCM/physical meter proof. edge=%s", secure, b)
}
