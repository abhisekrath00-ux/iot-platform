package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/eclipse/paho.mqtt.golang/packets"
)

// Characterization of an OPEN durability gap, not acceptance: telemetry commit
// and derived flow recording are not atomic. No channels/actuators are enabled.
func TestIntegrationCompiledIngestDerivedWorkGap(t *testing.T) {
	broker, ingest := os.Getenv("TEST_MOSQUITTO_BINARY"), os.Getenv("TEST_INGEST_BINARY")
	if broker == "" || ingest == "" {
		t.Skip("TEST_MOSQUITTO_BINARY and TEST_INGEST_BINARY required")
	}
	s, _ := testServer(t)
	const tenant = "itest-derived-gap"
	seed(t, s, tenant)
	ctx := t.Context()
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := s.st.Pool.Exec(cleanup, `DROP TRIGGER IF EXISTS itest_admission_gap_fault ON telemetry; DROP FUNCTION IF EXISTS itest_admission_gap_fault(); DROP TRIGGER IF EXISTS itest_derived_gap_fault ON flow_runs; DROP FUNCTION IF EXISTS itest_derived_gap_fault(); DELETE FROM flows WHERE tenant_id='itest-derived-gap'`); err != nil {
			t.Errorf("cleanup fixture: %v", err)
		}
	})
	for _, q := range []string{
		`DELETE FROM flows WHERE tenant_id='itest-derived-gap'`,
		`DELETE FROM telemetry WHERE tenant_id='itest-derived-gap'`,
		`INSERT INTO flows(id,tenant_id,name,definition,created_by) VALUES('itest-derived-gap-flow','itest-derived-gap','synthetic disabled delivery','{}','test-user')`,
		`INSERT INTO flow_versions(id,flow_id,tenant_id,version,definition,status,created_by) VALUES('itest-derived-gap-version','itest-derived-gap-flow','itest-derived-gap',1,'{"trigger":{"device_id":"itest-derived-gap-dev","point_id":"temp","op":">","value":100},"steps":[{"type":"notify","channel_id":"absent-no-delivery"}]}','published','test-user')`,
		`UPDATE flows SET published_version_id='itest-derived-gap-version' WHERE id='itest-derived-gap-flow'`,
		// A database fault, not a product test hook: only this synthetic flow fails.
		`CREATE OR REPLACE FUNCTION itest_derived_gap_fault() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.flow_id='itest-derived-gap-flow' THEN RAISE EXCEPTION 'synthetic derived gap fixture'; END IF; RETURN NEW; END$$`,
		`CREATE TRIGGER itest_derived_gap_fault BEFORE INSERT ON flow_runs FOR EACH ROW EXECUTE FUNCTION itest_derived_gap_fault()`,
	} {
		if _, err := s.st.Pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
	l.Close()
	conf := filepath.Join(dir, "broker.conf")
	if err = os.WriteFile(conf, []byte("listener "+port+" 127.0.0.1\nallow_anonymous true\npersistence false\n"), 0600); err != nil {
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
	if err = bc.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { bc.Process.Kill(); bc.Wait() }()
	wait := func(name string, predicate func() bool) {
		t.Helper()
		deadline := time.Now().Add(12 * time.Second)
		for {
			if predicate() {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("timeout", name)
			}
			time.Sleep(25 * time.Millisecond)
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
	// Decode and forward real MQTT packets on ingest's connection only. This
	// observes subscriber PUBACK, not the publisher's broker acceptance token.
	proxy, acked, deliveries := admissionWireProxy(t, "127.0.0.1:"+port)
	var process *exec.Cmd
	var log *os.File
	generation := 0
	stop := func() {
		if process != nil {
			process.Process.Kill()
			process.Wait()
			process = nil
		}
		if log != nil {
			log.Close()
			log = nil
		}
	}
	defer stop()
	start := func() {
		t.Helper()
		generation++
		var e error
		log, e = os.Create(filepath.Join(dir, fmt.Sprintf("ingest%d.log", generation)))
		if e != nil {
			t.Fatal(e)
		}
		process = exec.Command(ingest)
		process.Env = append(os.Environ(), "DATABASE_URL="+os.Getenv("TEST_DATABASE_URL"), "MQTT_HOST=127.0.0.1", "MQTT_PORT="+proxy, "MQTT_TLS=false", "MQTT_CA_FILE=", "MQTT_CERT_FILE=", "MQTT_KEY_FILE=", "GOOGLE_CLIENT_ID=", "SMTP_HOST=", "SLACK_BOT_TOKEN=", "SPARKPLUG_TENANT=", "INGEST_SHARED_GROUP=")
		process.Stdout = log
		process.Stderr = log
		if e = process.Start(); e != nil {
			t.Fatal(e)
		}
		wait("ingest subscriptions", func() bool {
			b, _ := os.ReadFile(log.Name())
			return strings.Contains(string(b), "ingest subscriptions ready")
		})
	}
	start()
	c := mqtt.NewClient(mqtt.NewClientOptions().AddBroker("tcp://127.0.0.1:" + port).SetClientID("synthetic-derived-gap-publisher"))
	tok := c.Connect()
	if !tok.WaitTimeout(3*time.Second) || tok.Error() != nil {
		t.Fatal("connect", tok.Error())
	}
	defer c.Disconnect(0)
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	send := func(id string, value int) {
		t.Helper()
		payload := fmt.Sprintf(`{"event_id":%q,"device_id":"itest-derived-gap-dev","point_id":"temp","value":%d,"unit":"C","observed_at":%q,"schema_version":1}`, id, value, stamp)
		tok := c.Publish("t/"+tenant+"/g/"+tenant+"-gw/telemetry", 1, false, payload)
		if !tok.WaitTimeout(3*time.Second) || tok.Error() != nil {
			t.Fatal("publish", tok.Error())
		}
	}
	for _, q := range []string{
		`CREATE FUNCTION itest_admission_gap_fault() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.event_id='itest-admission-gap-lost' THEN RAISE EXCEPTION 'synthetic telemetry admission refusal'; END IF; RETURN NEW; END$$`,
		`CREATE TRIGGER itest_admission_gap_fault BEFORE INSERT ON telemetry FOR EACH ROW EXECUTE FUNCTION itest_admission_gap_fault()`,
	} {
		if _, err = s.st.Pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	send("itest-admission-gap-lost", 80)
	wait("actual telemetry INSERT refusal", func() bool {
		b, _ := os.ReadFile(log.Name())
		return strings.Contains(string(b), "synthetic telemetry admission refusal")
	})
	wait("subscriber PUBACK for failed admission", func() bool { return acked("itest-admission-gap-lost") == 1 })
	var admitted int
	if err = s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM telemetry WHERE event_id='itest-admission-gap-lost'`).Scan(&admitted); err != nil || admitted != 0 {
		t.Fatalf("failed admission stored: %d %v", admitted, err)
	}
	if _, err = s.st.Pool.Exec(ctx, `DROP TRIGGER itest_admission_gap_fault ON telemetry; DROP FUNCTION itest_admission_gap_fault()`); err != nil {
		t.Fatal(err)
	}
	send("itest-derived-gap-lost", 120)
	wait("actual derived INSERT fault", func() bool {
		b, _ := os.ReadFile(log.Name())
		return strings.Contains(string(b), "synthetic derived gap fixture")
	})
	var stored int
	if err = s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM telemetry WHERE event_id='itest-derived-gap-lost'`).Scan(&stored); err != nil || stored != 1 {
		t.Fatal("telemetry did not commit", stored, err)
	}
	if _, err = s.st.Pool.Exec(ctx, `DROP TRIGGER itest_derived_gap_fault ON flow_runs; DROP FUNCTION itest_derived_gap_fault()`); err != nil {
		t.Fatal(err)
	}
	stop()
	start()
	send("itest-derived-gap-lost", 120)
	send("itest-derived-gap-barrier", 10)
	// Same MQTT publisher/topic + ordered handler: barrier's derived run proves
	// the replay passed through the restarted ingest, not a timed absence check.
	wait("post-restart barrier flow", func() bool {
		var n int
		s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM flow_runs WHERE flow_id='itest-derived-gap-flow' AND trigger_value=10`).Scan(&n)
		return n == 1
	})
	wait("post-restart subscriber barrier PUBACK", func() bool { return acked("itest-derived-gap-barrier") == 1 })
	if err = s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM telemetry WHERE event_id='itest-admission-gap-lost'`).Scan(&admitted); err != nil || admitted != 0 || deliveries("itest-admission-gap-lost") != 1 {
		t.Fatalf("admission gap changed: stored%d deliveries%d err%v", admitted, deliveries("itest-admission-gap-lost"), err)
	}
	t.Log("OPEN ADMISSION GAP: actual compiled ingest sent subscriber QoS1 PUBACK after telemetry INSERT refusal; row absent after fault removal/restart and unique ordered barrier, original delivery count1; publisher broker acceptance is not database durability; bounded default clean-session observation, no external effects")
	var lostRuns, events int
	if err = s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM flow_runs WHERE flow_id='itest-derived-gap-flow' AND trigger_value=120`).Scan(&lostRuns); err != nil {
		t.Fatal(err)
	}
	if err = s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM telemetry WHERE tenant_id='itest-derived-gap'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if lostRuns != 0 || events != 2 {
		t.Fatalf("gap characterization changed: lostRuns%d events%d", lostRuns, events)
	}
	t.Log("OPEN GAP reproduced with actual compiled ingest/PG/broker: telemetry committed, derived flow INSERT failed, restart+duplicate refused derived replay, next unique barrier derived; no external notification/output, not durability acceptance")
}

// admissionWireProxy uses no credentials, production broker or external routes.
// Counts only synthetically named events and returns no payloads to logs.
func admissionWireProxy(t *testing.T, upstream string) (string, func(string) int, func(string) int) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var sockets []net.Conn
	acknowledgements := map[string]int{}
	received := map[string]int{}
	t.Cleanup(func() {
		l.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range sockets {
			c.Close()
		}
	})
	go func() {
		for {
			in, e := l.Accept()
			if e != nil {
				return
			}
			out, e := net.DialTimeout("tcp", upstream, time.Second)
			if e != nil {
				in.Close()
				continue
			}
			mu.Lock()
			sockets = append(sockets, in, out)
			mu.Unlock()
			ids := &sync.Map{}
			pipe := func(src, dst net.Conn, towardIngest bool) {
				defer src.Close()
				defer dst.Close()
				for {
					p, e := packets.ReadPacket(src)
					if e != nil {
						return
					}
					if towardIngest {
						if pub, ok := p.(*packets.PublishPacket); ok {
							var env struct {
								EventID string `json:"event_id"`
							}
							if json.Unmarshal(pub.Payload, &env) == nil && strings.HasPrefix(env.EventID, "itest-") {
								ids.Store(pub.MessageID, env.EventID)
								mu.Lock()
								received[env.EventID]++
								mu.Unlock()
							}
						}
					}
					if e = p.Write(dst); e != nil {
						return
					}
					if !towardIngest {
						if ack, ok := p.(*packets.PubackPacket); ok {
							if id, found := ids.LoadAndDelete(ack.MessageID); found {
								mu.Lock()
								acknowledgements[id.(string)]++
								mu.Unlock()
							}
						}
					}
				}
			}
			go pipe(out, in, true)
			go pipe(in, out, false)
		}
	}()
	count := func(m map[string]int) func(string) int {
		return func(id string) int { mu.Lock(); defer mu.Unlock(); return m[id] }
	}
	return fmt.Sprint(l.Addr().(*net.TCPAddr).Port), count(acknowledgements), count(received)
}
