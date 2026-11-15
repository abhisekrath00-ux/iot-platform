package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/ingestprocessing"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/processingschema"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Test-only wrapper stops after the actual gate commits but BEFORE a real Ack.
type processingAckBarrier struct {
	mqtt.Message
	event string
	block bool
}

func (m processingAckBarrier) Ack() {
	fmt.Println("PROCESSING_BEFORE_ACK:" + m.event)
	if m.block {
		select {}
	}
	m.Message.Ack()
}

// Test process only: no production subscriber or option uses this harness.
func TestProcessingMQTTGateHelper(t *testing.T) {
	if os.Getenv("HEX_PROCESSING_MQTT_CHILD") != "1" {
		t.Skip("subprocess helper")
	}
	ctx := context.Background()
	st, err := store.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	gate, err := ingestprocessing.NewMQTTGate(ingestprocessing.Engine{Pool: st.Pool, Enabled: true}, 1)
	if err != nil {
		t.Fatal(err)
	}
	opts := mqtt.NewClientOptions().AddBroker("tcp://127.0.0.1:" + os.Getenv("HEX_PROCESSING_MQTT_PORT")).SetClientID(os.Getenv("HEX_PROCESSING_MQTT_ID")).SetCleanSession(false).SetAutoReconnect(false).SetAutoAckDisabled(true)
	opts.SetDefaultPublishHandler(func(_ mqtt.Client, m mqtt.Message) {
		var e ingestprocessing.Envelope
		_ = json.Unmarshal(m.Payload(), &e)
		c, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		_, err := gate.Handle(c, processingAckBarrier{Message: m, event: e.EventID, block: e.EventID == os.Getenv("HEX_PROCESSING_MQTT_STOP_BEFORE_ACK")})
		if err != nil {
			fmt.Println("PROCESSING_REFUSED:" + e.EventID + ":" + err.Error())
			return
		}
		fmt.Println("PROCESSING_ACK_CALLED:" + e.EventID)
	})
	client := mqtt.NewClient(opts)
	tok := client.Connect()
	if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatal("connect", tok.Error())
	}
	defer client.Disconnect(0)
	tok = client.Subscribe(os.Getenv("HEX_PROCESSING_MQTT_TOPIC"), 1, nil)
	if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatal("subscribe", tok.Error())
	}
	fmt.Println("PROCESSING_MQTT_READY")
	select {}
}

// Real plaintext live-broker test only. Subscriber SIGKILL is not broker crash,
// disk/power-loss, all-handler, TLS, source outbox or production acceptance.
func TestIntegrationProcessingMQTTGateTransport(t *testing.T) {

	broker := os.Getenv("TEST_MOSQUITTO_BINARY")
	if broker == "" {
		t.Skip("TEST_MOSQUITTO_BINARY required")
	}
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("processing_transport_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	cfg.MaxConns = 4
	cfg.MinConns = 0
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		p.Close()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(c, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Error(err)
		}
	}()
	childURL, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if childURL.Scheme != "postgres" && childURL.Scheme != "postgresql" {
		t.Fatal("transport fixture requires a PostgreSQL URL")
	}
	childURL.Path = "/" + name
	childDSN := childURL.String()
	t.Setenv("TEST_DATABASE_URL", childDSN)
	s := &server{st: &store.Store{Pool: p}}
	t.Setenv("MIGRATIONS_DIR", "../../migrations")
	if err = migrate(ctx, s.st); err != nil {
		t.Fatal(err)
	}
	const tenant = "itest-processing-mqtt-gate"
	seed(t, s, tenant)
	p.Reset()
	if _, err = processingschema.Apply(ctx, p, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, `INSERT INTO notification_channels(id,tenant_id,type,target,enabled) VALUES('staged-gate-channel',$1,'email','synthetic@example.invalid',true)`, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, `INSERT INTO flows(id,tenant_id,name,definition,created_by) VALUES('staged-gate-flow',$1,'Synthetic','{}','test-user');`, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, `INSERT INTO flow_versions(id,flow_id,tenant_id,version,definition,status,created_by) VALUES('staged-gate-version','staged-gate-flow',$1,1,'{"trigger":{"device_id":"itest-processing-mqtt-gate-dev","point_id":"temp","op":">","value":1},"steps":[{"type":"notify","channel_id":"staged-gate-channel","message":"synthetic"}]}','published','test-user')`, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, `UPDATE flows SET published_version_id='staged-gate-version' WHERE id='staged-gate-flow'`); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
	l.Close()
	dataDir := filepath.Join(dir, "broker-data")
	if err := os.Mkdir(dataDir, 0700); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(dir, "broker.conf")
	persistence := "persistence false\n"
	if err := os.WriteFile(conf, []byte("listener "+port+" 127.0.0.1\nallow_anonymous true\n"+persistence+"max_inflight_messages 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	bl, err := os.Create(filepath.Join(dir, "broker.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer bl.Close()
	bc := exec.Command(broker, "-c", conf)
	bc.Stdout = bl
	bc.Stderr = bl
	if err := bc.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { bc.Process.Kill(); bc.Wait() }()
	wait := func(name string, p func() bool) {
		t.Helper()
		deadline := time.Now().Add(12 * time.Second)
		for {
			if p() {
				return
			}
			if time.Now().After(deadline) {
				b, _ := os.ReadFile(bl.Name())
				logs, _ := filepath.Glob(filepath.Join(dir, "processing-*.log"))
				for _, log := range logs {
					raw, _ := os.ReadFile(log)
					t.Logf("child=%s", raw)
				}
				t.Fatalf("timeout %s broker=%s", name, b)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	wait("broker", func() bool {
		c, e := net.DialTimeout("tcp", "127.0.0.1:"+port, 100*time.Millisecond)
		if e == nil {
			c.Close()
			return true
		}
		return false
	})
	proxy, acked, deliveries := admissionWireProxy(t, "127.0.0.1:"+port)
	topic := "t/" + tenant + "/g/" + tenant + "-gw/telemetry"
	var child *exec.Cmd
	var childLog *os.File
	generation := 0
	stop := func() {
		if child != nil {
			child.Process.Kill()
			child.Wait()
			child = nil
		}
		if childLog != nil {
			childLog.Close()
			childLog = nil
		}
	}
	defer stop()
	start := func(blockID string) {
		t.Helper()
		generation++
		var err error
		childLog, err = os.Create(filepath.Join(dir, fmt.Sprintf("processing-%d.log", generation)))
		if err != nil {
			t.Fatal(err)
		}
		child = exec.Command(os.Args[0], "-test.run=^TestProcessingMQTTGateHelper$", "-test.timeout=50s")
		child.Env = append(os.Environ(), "HEX_PROCESSING_MQTT_CHILD=1", "HEX_PROCESSING_MQTT_PORT="+proxy, "HEX_PROCESSING_MQTT_ID=synthetic-persistent-processing", "HEX_PROCESSING_MQTT_TOPIC="+topic, "HEX_PROCESSING_MQTT_STOP_BEFORE_ACK="+blockID)
		child.Stdout = childLog
		child.Stderr = childLog
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		wait("candidate ready", func() bool {
			b, _ := os.ReadFile(childLog.Name())
			return strings.Contains(string(b), "PROCESSING_MQTT_READY")
		})
	}
	publisher := mqtt.NewClient(mqtt.NewClientOptions().AddBroker("tcp://127.0.0.1:" + port).SetClientID("synthetic-admission-publisher").SetAutoReconnect(false))
	tok := publisher.Connect()
	if !tok.WaitTimeout(3*time.Second) || tok.Error() != nil {
		t.Fatal(tok.Error())
	}
	defer publisher.Disconnect(0)
	e := ingestprocessing.Envelope{EventID: "itest-mqtt-admission-refused", TenantID: tenant, GatewayID: tenant + "-gw", DeviceID: tenant + "-dev", PointID: "temp", ObservedAt: time.Now().UTC().Truncate(time.Microsecond), Value: 42, Quality: "measured", SchemaVersion: 1}
	send := func(e ingestprocessing.Envelope) {
		t.Helper()
		b, _ := json.Marshal(e)
		tok := publisher.Publish(topic, 1, false, b)
		if !tok.WaitTimeout(3*time.Second) || tok.Error() != nil {
			t.Fatal("publish", tok.Error())
		}
	}
	count := func(event string) int {
		t.Helper()
		var n int
		if err := s.st.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM telemetry WHERE event_id=$1)+(SELECT count(*) FROM ingest_processing_v1 WHERE event_id=$1)`, event).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if _, err := s.st.Pool.Exec(ctx, `CREATE FUNCTION itest_mqtt_admission_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event_id='itest-mqtt-admission-refused' THEN RAISE EXCEPTION 'synthetic DB refusal'; END IF; RETURN NEW; END $$;CREATE TRIGGER itest_mqtt_admission_fault BEFORE INSERT ON telemetry FOR EACH ROW EXECUTE FUNCTION itest_mqtt_admission_fault()`); err != nil {
		t.Fatal(err)
	}
	start("")
	send(e)
	wait("refused callback", func() bool {
		b, _ := os.ReadFile(childLog.Name())
		return strings.Contains(string(b), "PROCESSING_REFUSED:"+e.EventID)
	})
	if acked(e.EventID) != 0 || count(e.EventID) != 0 {
		t.Fatal("refused admission acknowledged or committed", acked(e.EventID), count(e.EventID))
	}
	stop()
	if _, err := s.st.Pool.Exec(ctx, `DROP TRIGGER itest_mqtt_admission_fault ON telemetry`); err != nil {
		t.Fatal(err)
	}
	start("")
	wait("redelivery admitted and acknowledged", func() bool { return count(e.EventID) == 2 && acked(e.EventID) == 1 })
	if deliveries(e.EventID) < 2 {
		t.Fatal("missing broker redelivery", deliveries(e.EventID))
	}
	j := ingestprocessing.Engine{Pool: s.st.Pool, Enabled: true}
	w, err := j.Read(ctx, tenant, e.EventID, e.ObservedAt)
	if err != nil || w.State != "pending" || w.AttemptID != "" {
		t.Fatal(w, err)
	}
	// Kill the subscriber after a committed pair but before its PUBACK.
	stop()
	e.EventID = "itest-mqtt-admission-committed-no-ack"
	start(e.EventID)
	send(e)
	wait("committed before ACK", func() bool {
		b, _ := os.ReadFile(childLog.Name())
		return strings.Contains(string(b), "PROCESSING_BEFORE_ACK:"+e.EventID) && count(e.EventID) == 2
	})
	if acked(e.EventID) != 0 {
		t.Fatal("ACK before test barrier")
	}
	before, err := j.Read(ctx, tenant, e.EventID, e.ObservedAt)
	if err != nil {
		t.Fatal(err)
	}
	retained, _ := json.Marshal(before.Plan)
	stop()
	// Existing work may already be terminal. Replay can acknowledge retained
	// admission but must never turn terminal work back into pending.
	// A separately staged unknown state must survive exact transport replay.
	attempt := "12345678-1234-1234-1234-123456789012"
	if _, err = s.st.Pool.Exec(ctx, `UPDATE ingest_processing_v1 SET state='outcome_unknown',attempt_id=$2 WHERE event_id=$1`, e.EventID, attempt); err != nil {
		t.Fatal(err)
	}
	if _, err = s.st.Pool.Exec(ctx, `UPDATE notification_channels SET target='changed@example.invalid' WHERE id='staged-gate-channel'`); err != nil {
		t.Fatal(err)
	}
	start("")
	wait("committed replay ACK", func() bool { return acked(e.EventID) == 1 && deliveries(e.EventID) >= 2 })
	if count(e.EventID) != 2 {
		t.Fatal("duplicate pair")
	}
	after, err := j.Read(ctx, tenant, e.EventID, e.ObservedAt)
	if err != nil || after.State != "outcome_unknown" || after.AttemptID != attempt {
		t.Fatal("rearmed terminal admission", after, err)
	}
	// An ordered unique event follows the recovered unacked input.
	barrier := e
	barrier.EventID = "itest-mqtt-admission-barrier"
	send(barrier)
	wait("barrier", func() bool { return count(barrier.EventID) == 2 && acked(barrier.EventID) == 1 })
	if acked(e.EventID) != 1 {
		t.Fatal("extra observed ACK", acked(e.EventID))
	}

	replayPlan, _ := json.Marshal(after.Plan)
	if !bytes.Equal(retained, replayPlan) {
		t.Fatal("replay changed complete retained plan")
	}
	var history int
	if err = p.QueryRow(ctx, `SELECT count(*) FROM flow_runs r JOIN flows f ON f.id=r.flow_id WHERE f.tenant_id=$1`, tenant).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if history != 0 {
		t.Fatal("derived work history created", history)
	}
	if len(after.Plan.Intents) != 1 || after.Plan.Intents[0].Target != "synthetic@example.invalid" {
		t.Fatal("retained plan changed", after)
	}
	t.Log("isolated staged gate: actual wire no PUBACK on insert refusal; stable persistent subscriber restart redelivers; committed-before-ACK SIGKILL reconciles one retained unknown plan without rearm or derived work; live plaintext broker only, production unchanged")
}
