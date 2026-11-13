package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/mqttconfig"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/golang-jwt/jwt/v5"
)

// Opt in with a disposable TEST_DATABASE_URL, loopback TEST_MQTT_PORT and
// TEST_INGEST_BINARY (built from ./cmd/ingest). No external services/delivery.
func TestIntegrationMQTTPipeline(t *testing.T) { runMQTTPipeline(t, nil) }

func runMQTTPipeline(t *testing.T, secure map[string]string) {
	binary, port := os.Getenv("TEST_INGEST_BINARY"), os.Getenv("TEST_MQTT_PORT")
	if secure != nil {
		port = secure["MQTT_PORT"]
	}
	if binary == "" || port == "" {
		t.Skip("TEST_INGEST_BINARY and TEST_MQTT_PORT required (local broker)")
	}
	brokerDeadline := time.Now().Add(15 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(brokerDeadline) {
			t.Fatal("local MQTT listener not ready", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	s, _ := testServer(t)
	s.cache = newCache()
	const tenant = "itest-mqpipe"
	seed(t, s, tenant)
	seed(t, s, "itest-mqother")
	ctx := t.Context()
	if _, err := s.st.Pool.Exec(ctx, `DELETE FROM telemetry WHERE tenant_id IN ('itest-mqpipe','itest-mqother')`); err != nil {
		t.Fatal(err)
	}
	// A local TCP cut exercises the real client's reconnect, without stopping
	// or mutating the broker shared by other tests.
	proxy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var sockets []net.Conn
	t.Cleanup(func() {
		proxy.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range sockets {
			c.Close()
		}
	})
	go func() {
		for {
			incoming, err := proxy.Accept()
			if err != nil {
				return
			}
			upstream, err := net.Dial("tcp", "127.0.0.1:"+port)
			if err != nil {
				incoming.Close()
				continue
			}
			mu.Lock()
			sockets = append(sockets, incoming, upstream)
			mu.Unlock()
			go func() {
				defer incoming.Close()
				defer upstream.Close()
				done := make(chan struct{})
				go func() { io.Copy(upstream, incoming); close(done) }()
				io.Copy(incoming, upstream)
				incoming.Close()
				<-done
			}()
		}
	}()
	ingestPort := fmt.Sprint(proxy.Addr().(*net.TCPAddr).Port)
	logPath := filepath.Join(t.TempDir(), "ingest.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), "DATABASE_URL="+os.Getenv("TEST_DATABASE_URL"), "MQTT_HOST=127.0.0.1", "MQTT_TLS=false", "MQTT_CA_FILE=", "MQTT_CERT_FILE=", "MQTT_KEY_FILE=", "MQTT_PORT="+ingestPort, "GOOGLE_CLIENT_ID=", "SPARKPLUG_TENANT=", "INGEST_SHARED_GROUP=", "SMTP_HOST=", "SLACK_BOT_TOKEN=")
	if secure != nil {
		for _, key := range []string{"MQTT_TLS", "MQTT_CA_FILE", "MQTT_CERT_FILE", "MQTT_KEY_FILE"} {
			cmd.Env = append(cmd.Env, key+"="+secure[key])
		}
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	deadline := time.Now().Add(15 * time.Second)
	for {
		b, _ := os.ReadFile(logPath)
		if strings.Contains(string(b), "ingest subscriptions ready") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ingest readiness timeout: %s", b)
		}
		time.Sleep(50 * time.Millisecond)
	}
	publisherOpts := mqtt.NewClientOptions().AddBroker("tcp://127.0.0.1:" + port).SetClientID(fmt.Sprintf("test-pipe-%d", time.Now().UnixNano()))
	if secure != nil {
		cfg := map[string]string{}
		for k, v := range secure {
			cfg[k] = v
		}
		cfg["MQTT_CERT_FILE"], cfg["MQTT_KEY_FILE"] = secure["PUBLISHER_CERT"], secure["PUBLISHER_KEY"]
		var err error
		publisherOpts, err = mqttconfig.Options(func(k string) string { return cfg[k] }, fmt.Sprintf("test-tls-pipe-%d", time.Now().UnixNano()))
		if err != nil {
			t.Fatal(err)
		}
	}
	c := mqtt.NewClient(publisherOpts)
	if tok := c.Connect(); !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatal("publisher connect", tok.Error())
	}
	defer c.Disconnect(100)
	// Disconnect only ingest. Its default clean session is discarded by the
	// broker; reconnect must explicitly restore all subscriptions.
	for reconnect := 0; reconnect < 2; reconnect++ {
		mu.Lock()
		for _, socket := range sockets {
			socket.Close()
		}
		mu.Unlock()
		deadline = time.Now().Add(20 * time.Second)
		for {
			b, _ := os.ReadFile(logPath)
			if strings.Count(string(b), "ingest subscriptions ready") >= reconnect+2 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("reconnect subscriptions timeout: %s", b)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	// Exercise non-telemetry subscriptions after reconnect. These are seeded
	// state records and synthetic inbound replies, never requests to an actuator.
	if _, err := s.st.Pool.Exec(ctx, `DELETE FROM gateway_scans WHERE tenant_id=$1;`, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := s.st.Pool.Exec(ctx, `DELETE FROM commands WHERE request_id LIKE 'mqpipe-%'`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"scan", "scan-barrier"} {
		if _, err := s.st.Pool.Exec(ctx, `INSERT INTO gateway_scans(id,tenant_id,gateway_id,kind,status) VALUES($1,$2,$3,'lan','requested')`, "mqpipe-"+id, tenant, tenant+"-gw"); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct{ id, status string }{{"cmd", "sent"}, {"pending", "pending_approval"}, {"rejected", "sent"}, {"cmd-barrier", "sent"}} {
		if _, err := s.st.Pool.Exec(ctx, `INSERT INTO commands(request_id,tenant_id,gateway_id,device_id,action,status,requested_by) VALUES($1,$2,$3,$4,'synthetic.noop',$5,'test-user')`, "mqpipe-"+row.id, tenant, tenant+"-gw", tenant+"-dev", row.status); err != nil {
			t.Fatal(err)
		}
	}
	publishReply := func(topic string, body any) {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		tok := c.Publish(topic, 1, false, b)
		if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
			t.Fatal("reply publish", tok.Error())
		}
	}
	topic := "t/" + tenant + "/g/" + tenant + "-gw/"
	// Wrong topic identity must not settle otherwise valid IDs.
	for _, prefix := range []string{"t/itest-mqother/g/" + tenant + "-gw/", "t/" + tenant + "/g/wrong-gateway/"} {
		publishReply(prefix+"scan/result", map[string]any{"scan_id": "mqpipe-scan", "ok": true, "hosts": []any{map[string]any{"Addr": "10.0.0.99", "Port": 502, "Service": "wrong identity"}}})
		publishReply(prefix+"cmd/ack", map[string]any{"request_id": "mqpipe-cmd", "state": "acked", "detail": "wrong identity"})
	}
	publishReply(topic+"scan/result", map[string]any{"scan_id": "mqpipe-scan-barrier", "ok": true})
	publishReply(topic+"cmd/ack", map[string]any{"request_id": "mqpipe-cmd-barrier", "state": "acked"})
	waitState := func(table, key, id, want string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			var got string
			err := s.st.Pool.QueryRow(ctx, "SELECT status FROM "+table+" WHERE "+key+"=$1", id).Scan(&got)
			if err != nil {
				t.Fatal(err)
			}
			if got == want {
				return
			}
			if time.Now().After(deadline) {
				b, _ := os.ReadFile(logPath)
				t.Fatalf("%s state=%s want%s: %s", id, got, want, b)
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	waitState("gateway_scans", "id", "mqpipe-scan-barrier", "done")
	waitState("commands", "request_id", "mqpipe-cmd-barrier", "acked")
	var scanStatus, cmdStatus string
	if err := s.st.Pool.QueryRow(ctx, `SELECT status FROM gateway_scans WHERE id='mqpipe-scan'`).Scan(&scanStatus); err != nil {
		t.Fatal(err)
	}
	if err := s.st.Pool.QueryRow(ctx, `SELECT status FROM commands WHERE request_id='mqpipe-cmd'`).Scan(&cmdStatus); err != nil {
		t.Fatal(err)
	}
	if scanStatus != "requested" || cmdStatus != "sent" {
		t.Fatal("wrong identity settled state", scanStatus, cmdStatus)
	}
	publishReply(topic+"scan/result", map[string]any{"scan_id": "mqpipe-scan", "ok": true, "hosts": []any{map[string]any{"Addr": "10.0.0.9", "Port": 502, "Service": "Synthetic Modbus"}}})
	publishReply(topic+"cmd/ack", map[string]any{"request_id": "mqpipe-cmd", "state": "acked", "detail": "synthetic accepted"})
	publishReply(topic+"cmd/ack", map[string]any{"request_id": "mqpipe-pending", "state": "acked"})
	publishReply(topic+"cmd/ack", map[string]any{"request_id": "mqpipe-rejected", "state": "rejected", "detail": "synthetic refusal"})
	waitState("gateway_scans", "id", "mqpipe-scan", "done")
	waitState("commands", "request_id", "mqpipe-cmd", "acked")
	waitState("commands", "request_id", "mqpipe-rejected", "failed")
	// Conflicting late terminal replies must not overwrite the first outcome.
	publishReply(topic+"scan/result", map[string]any{"scan_id": "mqpipe-scan", "ok": false, "error": "late overwrite"})
	publishReply(topic+"cmd/ack", map[string]any{"request_id": "mqpipe-cmd", "state": "rejected", "detail": "late overwrite"})
	at := time.Now().UTC().Truncate(time.Millisecond)
	send := func(event string, value float64, payloadTenant string) {
		stamp := at
		if event == "mq-barrier" {
			stamp = at.Add(time.Second)
		}
		p := map[string]any{"event_id": event, "device_id": tenant + "-dev", "point_id": "temp", "observed_at": stamp, "value": value, "unit": "C", "schema_version": 1}
		if payloadTenant != "" {
			p["tenant_id"] = payloadTenant
		}
		b, _ := json.Marshal(p)
		tok := c.Publish("t/"+tenant+"/g/"+tenant+"-gw/telemetry", 1, false, b)
		if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
			t.Fatal("publish", tok.Error())
		}
	}
	send("mq-pipeline-1", 42.5, "")
	send("mq-pipeline-1", 42.5, "")
	send("mq-spoof", 999, "itest-mqother")
	if secure != nil {
		publishReply("t/itest-mqother/g/itest-mqother-gw/telemetry", map[string]any{"event_id": "mq-tls-foreign", "device_id": "itest-mqother-dev", "point_id": "temp", "observed_at": at, "value": 999, "unit": "C", "schema_version": 1})
	}
	send("mq-barrier", 43.5, "")
	deadline = time.Now().Add(10 * time.Second)
	for {
		var n int
		err := s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM telemetry WHERE event_id='mq-barrier'`).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			b, _ := os.ReadFile(logPath)
			t.Fatalf("MQTT-to-SQL timeout: %s", b)
		}
		time.Sleep(25 * time.Millisecond)
	}
	var count, spoof int
	if err := s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM telemetry WHERE tenant_id=$1`, tenant).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM telemetry WHERE event_id='mq-spoof'`).Scan(&spoof); err != nil {
		t.Fatal(err)
	}
	if secure != nil {
		var foreign int
		if err := s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM telemetry WHERE event_id='mq-tls-foreign'`).Scan(&foreign); err != nil || foreign != 0 {
			t.Fatalf("broker ACL foreign telemetry: %d %v", foreign, err)
		}
	}
	if count != 2 || spoof != 0 {
		t.Fatalf("dedupe/spoof: count%d spoof%d", count, spoof)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/gateways/{id}/scans", s.listScans)
	mux.HandleFunc("GET /v1/commands", s.listCommands)
	s.cached(mux, "GET /v1/telemetry/latest", s.latestTelemetry)
	mux.HandleFunc("GET /v1/telemetry/series", s.seriesTelemetry)
	secret := []byte("synthetic-test-key-not-for-production")
	httpServer := httptest.NewServer(auth.Middleware(secret)(mux))
	defer httpServer.Close()
	get := func(tenant, path string) (int, string) {
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{TenantID: tenant, Role: "viewer", RegisteredClaims: jwt.RegisteredClaims{Subject: "synthetic", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}}).SignedString(secret)
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest("GET", httpServer.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	code, body := get(tenant, "/v1/telemetry/latest?device_id="+tenant+"-dev")
	if code != 200 || !strings.Contains(body, `"value":43.5`) || !strings.Contains(body, `"quality":"measured"`) {
		t.Fatalf("latest %d %s", code, body)
	}
	code, body = get(tenant, "/v1/telemetry/series?device_id="+tenant+"-dev&point_id=temp&hours=1")
	if code != 200 || !strings.Contains(body, "42.5") || !strings.Contains(body, "43.5") {
		t.Fatalf("series %d %s", code, body)
	}
	code, body = get("itest-mqother", "/v1/telemetry/latest?device_id="+tenant+"-dev")
	if code != 200 || strings.TrimSpace(body) != "[]" {
		t.Fatalf("tenant leak %d %s", code, body)
	}
	code, body = get(tenant, "/v1/gateways/"+tenant+"-gw/scans")
	if code != 200 || !strings.Contains(body, "Synthetic Modbus") || strings.Contains(body, "late overwrite") || strings.Contains(body, "wrong identity") {
		t.Fatalf("scan read %d %s", code, body)
	}
	code, body = get("itest-mqother", "/v1/gateways/"+tenant+"-gw/scans")
	if code != 200 || strings.TrimSpace(body) != "[]" {
		t.Fatal("scan tenant leak", code, body)
	}
	code, body = get(tenant, "/v1/commands")
	if code != 200 || !strings.Contains(body, `"status":"acked"`) {
		t.Fatal("commands read", code, body)
	}
	code, body = get("itest-mqother", "/v1/commands")
	if code != 200 || strings.Contains(body, "mqpipe-") {
		t.Fatal("command tenant leak", code, body)
	}
	var outcome []byte
	if err := s.st.Pool.QueryRow(ctx, `SELECT status,outcome FROM commands WHERE request_id='mqpipe-cmd'`).Scan(&cmdStatus, &outcome); err != nil {
		t.Fatal(err)
	}
	if cmdStatus != "acked" || !strings.Contains(string(outcome), "synthetic accepted") || strings.Contains(string(outcome), "late overwrite") {
		t.Fatal("terminal command overwritten", cmdStatus, string(outcome))
	}
	if err := s.st.Pool.QueryRow(ctx, `SELECT status FROM commands WHERE request_id='mqpipe-pending'`).Scan(&cmdStatus); err != nil || cmdStatus != "pending_approval" {
		t.Fatal("pending command settled", err, cmdStatus)
	}
	t.Log("post-reconnect manual scan results and synthetic command ACKs: topic tenant/gateway isolation, pending-command gate, rejected->failed, first terminal outcome and JWT scoped reads passed; no actuators")
	res, err := http.Get(httpServer.URL + "/v1/telemetry/latest")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal("missing JWT", res.StatusCode)
	}
	if secure != nil {
		mixedMQTTReads(t, s, c, httpServer.URL, secret, func() error {
			b, _ := os.ReadFile(logPath)
			before := strings.Count(string(b), "ingest subscriptions ready")
			mu.Lock()
			for _, socket := range sockets {
				socket.Close()
			}
			mu.Unlock()
			end := time.Now().Add(20 * time.Second)
			for time.Now().Before(end) {
				b, _ := os.ReadFile(logPath)
				if strings.Count(string(b), "ingest subscriptions ready") > before {
					return nil
				}
				time.Sleep(25 * time.Millisecond)
			}
			return fmt.Errorf("mixed-flow ingest subscription recovery timed out")
		})
	}
	if secure != nil {
		t.Log("certificate-required TLS broker, CN-scoped gateway ACL -> compiled ingest after two TLS reconnects -> Postgres -> JWT HTTP passed; foreign topic publications withheld by broker")
	}
	t.Log("local broker -> real ingest process after TCP disconnect/reconnect -> Postgres -> JWT-verified latest/series handlers passed; duplicate pair once, payload tenant spoof dropped")
}
