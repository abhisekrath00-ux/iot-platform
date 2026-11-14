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
	"syscall"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/ingestjournal"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// Test process only: not compiled ingest, a product option or runtime worker.
func TestAdmissionMQTTCandidateHelper(t *testing.T) {
	if os.Getenv("HEX_MQTT_ADMISSION_CHILD") != "1" {
		t.Skip("subprocess helper")
	}
	ctx := context.Background()
	st, err := store.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	j := ingestjournal.Journal{Pool: st.Pool}
	opts := mqtt.NewClientOptions().AddBroker("tcp://127.0.0.1:" + os.Getenv("HEX_MQTT_ADMISSION_PORT")).SetClientID(os.Getenv("HEX_MQTT_ADMISSION_ID")).SetCleanSession(false).SetAutoReconnect(false).SetAutoAckDisabled(true)
	opts.SetDefaultPublishHandler(func(_ mqtt.Client, m mqtt.Message) {
		var e ingestjournal.Envelope
		if json.Unmarshal(m.Payload(), &e) != nil {
			fmt.Println("CANDIDATE_BAD_INPUT")
			return
		}
		c, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		fresh, err := j.Admit(c, e)
		if err != nil {
			fmt.Printf("CANDIDATE_REFUSED:%s\n", e.EventID)
			return
		}
		fmt.Printf("CANDIDATE_COMMITTED:%s:%t\n", e.EventID, fresh)
		if e.EventID == os.Getenv("HEX_MQTT_ADMISSION_STOP_BEFORE_ACK") {
			select {}
		}
		m.Ack()
		fmt.Printf("CANDIDATE_ACK_CALLED:%s\n", e.EventID)
	})
	c := mqtt.NewClient(opts)
	tok := c.Connect()
	if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatal("connect", tok.Error())
	}
	defer c.Disconnect(0)
	tok = c.Subscribe(os.Getenv("HEX_MQTT_ADMISSION_TOPIC"), 1, nil)
	if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatal("subscribe", tok.Error())
	}
	fmt.Println("CANDIDATE_MQTT_READY")
	select {}
}

// Candidate transport experiments ONLY. The default broker stays alive; the
// separate variants test graceful file reload and checkpointed process kill.
// None accepts power loss, fleet, TLS or production ingest behavior.
func TestIntegrationMQTTAdmissionCandidateBoundary(t *testing.T) {
	runMQTTAdmissionCandidate(t, "memory")
}

// Graceful broker persistence is separate from the live in-memory boundary.
// This does not prove power-loss/fsync/disk-failure or production config safety.
func TestIntegrationMQTTAdmissionCandidateBrokerRestart(t *testing.T) {
	runMQTTAdmissionCandidate(t, "graceful")
}

// Process kill after an explicit broker checkpoint is not power-loss proof.
func TestIntegrationMQTTAdmissionCandidateBrokerCrash(t *testing.T) {
	runMQTTAdmissionCandidate(t, "checkpoint-kill")
}

func runMQTTAdmissionCandidate(t *testing.T, brokerMode string) {
	brokerRestart := brokerMode != "memory"
	broker := os.Getenv("TEST_MOSQUITTO_BINARY")
	if broker == "" {
		t.Skip("TEST_MOSQUITTO_BINARY required")
	}
	s, _ := testServer(t)
	ctx := t.Context()
	const tenant = "itest-mqtt-admission-candidate"
	seed(t, s, tenant)
	schema, err := os.ReadFile("../../internal/ingestjournal/candidate.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.st.Pool.Exec(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := s.st.Pool.Exec(c, `DROP TRIGGER IF EXISTS itest_mqtt_admission_fault ON telemetry;DROP FUNCTION IF EXISTS itest_mqtt_admission_fault();DROP TABLE IF EXISTS ingest_admission_candidate`); err != nil {
			t.Error(err)
		}
	})
	dir := t.TempDir()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
	l.Close()
	conf := filepath.Join(dir, "broker.conf")
	persistence := "persistence false\n"
	if brokerRestart {
		persistence = "persistence true\npersistence_location " + dir + "/\npersistence_file candidate.db\nautosave_interval 0\n"
	}
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
		childLog, err = os.Create(filepath.Join(dir, fmt.Sprintf("candidate-%d.log", generation)))
		if err != nil {
			t.Fatal(err)
		}
		child = exec.Command(os.Args[0], "-test.run=^TestAdmissionMQTTCandidateHelper$", "-test.timeout=50s")
		child.Env = append(os.Environ(), "HEX_MQTT_ADMISSION_CHILD=1", "HEX_MQTT_ADMISSION_PORT="+proxy, "HEX_MQTT_ADMISSION_ID=synthetic-persistent-admission", "HEX_MQTT_ADMISSION_TOPIC="+topic, "HEX_MQTT_ADMISSION_STOP_BEFORE_ACK="+blockID)
		child.Stdout = childLog
		child.Stderr = childLog
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		wait("candidate ready", func() bool {
			b, _ := os.ReadFile(childLog.Name())
			return strings.Contains(string(b), "CANDIDATE_MQTT_READY")
		})
	}
	publisher := mqtt.NewClient(mqtt.NewClientOptions().AddBroker("tcp://127.0.0.1:" + port).SetClientID("synthetic-admission-publisher").SetAutoReconnect(false))
	tok := publisher.Connect()
	if !tok.WaitTimeout(3*time.Second) || tok.Error() != nil {
		t.Fatal(tok.Error())
	}
	defer publisher.Disconnect(0)
	e := ingestjournal.Envelope{EventID: "itest-mqtt-admission-refused", TenantID: tenant, GatewayID: tenant + "-gw", DeviceID: tenant + "-dev", PointID: "temp", ObservedAt: time.Now().UTC().Truncate(time.Microsecond), Value: 42, Quality: "measured", SchemaVersion: 1}
	send := func(e ingestjournal.Envelope) {
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
		if err := s.st.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM telemetry WHERE event_id=$1)+(SELECT count(*) FROM ingest_admission_candidate WHERE event_id=$1)`, event).Scan(&n); err != nil {
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
		return strings.Contains(string(b), "CANDIDATE_REFUSED:"+e.EventID)
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
	j := ingestjournal.Journal{Pool: s.st.Pool}
	w, err := j.ReadWork(ctx, tenant, e.EventID, e.ObservedAt)
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
		return strings.Contains(string(b), "CANDIDATE_COMMITTED:"+e.EventID+":true") && count(e.EventID) == 2
	})
	if acked(e.EventID) != 0 {
		t.Fatal("ACK before test barrier")
	}
	stop()
	// Existing work may already be terminal. Replay can acknowledge retained
	// admission but must never turn terminal work back into pending.
	w, err = j.Claim(ctx, tenant, e.EventID, e.ObservedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err = j.MarkUnknown(ctx, tenant, e.EventID, e.ObservedAt, w.AttemptID); err != nil {
		t.Fatal(err)
	}
	start("")
	wait("committed replay ACK", func() bool { return acked(e.EventID) == 1 && deliveries(e.EventID) >= 2 })
	if count(e.EventID) != 2 {
		t.Fatal("duplicate pair")
	}
	after, err := j.ReadWork(ctx, tenant, e.EventID, e.ObservedAt)
	if err != nil || after.State != "outcome_unknown" || after.AttemptID != w.AttemptID {
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
	// A tiny offline queue is bounded evidence, not throughput/backpressure
	// acceptance. Only the explicit variant restarts the file-backed broker.
	stop()
	queued := make([]ingestjournal.Envelope, 3)
	for i := range queued {
		queued[i] = e
		queued[i].EventID = fmt.Sprintf("itest-mqtt-admission-offline-%d", i)
		send(queued[i])
		if count(queued[i].EventID) != 0 {
			t.Fatal("offline event admitted without subscriber")
		}
	}
	var uncheckpointed ingestjournal.Envelope
	if brokerRestart {
		// Restart the same binary/config/data, not a fresh broker. The crash
		// variant checkpoints explicitly before SIGKILL; graceful uses SIGINT.
		if brokerMode == "checkpoint-kill" {
			// SIGUSR1 requests a broker checkpoint, not delivery/DB acceptance. The
			// replay assertions below determine whether the queued data survived.
			checkpointAt := time.Now()
			if err := bc.Process.Signal(syscall.SIGUSR1); err != nil {
				t.Fatal(err)
			}
			wait("broker explicit checkpoint", func() bool {
				f, err := os.Stat(filepath.Join(dir, "candidate.db"))
				return err == nil && f.Size() > 0 && !f.ModTime().Before(checkpointAt)
			})
			// Publisher acceptance after that checkpoint is deliberately not
			// on disk. A killed broker can lose it despite the durable candidate DB.
			uncheckpointed = e
			uncheckpointed.EventID = "itest-mqtt-admission-after-checkpoint"
			send(uncheckpointed)
			if count(uncheckpointed.EventID) != 0 {
				t.Fatal("offline input admitted")
			}
			if err := bc.Process.Kill(); err != nil {
				t.Fatal(err)
			}
		} else if err := bc.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
		stopped := make(chan error, 1)
		go func() { stopped <- bc.Wait() }()
		select {
		case err := <-stopped:
			if brokerMode == "checkpoint-kill" {
				status, ok := bc.ProcessState.Sys().(syscall.WaitStatus)
				if err == nil || !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
					t.Fatal("expected broker SIGKILL termination", err, bc.ProcessState)
				}
			}
			if brokerMode != "checkpoint-kill" && err != nil {
				t.Fatal("graceful broker stop", err)
			}
		case <-time.After(5 * time.Second):
			bc.Process.Kill()
			<-stopped
			t.Fatal("broker graceful stop timeout")
		}
		info, err := os.Stat(filepath.Join(dir, "candidate.db"))
		if err != nil || info.Size() == 0 {
			t.Fatal("missing broker persistence file", err)
		}
		bc = exec.Command(broker, "-c", conf)
		bc.Stdout = bl
		bc.Stderr = bl
		if err := bc.Start(); err != nil {
			t.Fatal(err)
		}
		wait("restarted broker", func() bool {
			c, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 100*time.Millisecond)
			if err == nil {
				c.Close()
				return true
			}
			return false
		})
	}
	start("")
	for _, q := range queued {
		wait("offline queue replay", func() bool { return count(q.EventID) == 2 && acked(q.EventID) == 1 })
		qw, err := j.ReadWork(ctx, tenant, q.EventID, q.ObservedAt)
		if err != nil || qw.State != "pending" || qw.AttemptID != "" {
			t.Fatal("offline state", qw, err)
		}
	}
	if brokerMode == "checkpoint-kill" {
		liveBarrier := e
		liveBarrier.EventID = "itest-mqtt-admission-after-broker-restart"
		// Publisher's prior TCP connection was killed too. Reconnect explicitly.
		if publisher.IsConnected() {
			publisher.Disconnect(0)
		}
		tok = publisher.Connect()
		if !tok.WaitTimeout(3*time.Second) || tok.Error() != nil {
			t.Fatal("publisher reconnect", tok.Error())
		}
		send(liveBarrier)
		wait("post-broker-restart ordered barrier", func() bool { return count(liveBarrier.EventID) == 2 && acked(liveBarrier.EventID) == 1 })
		if count(uncheckpointed.EventID) != 0 || deliveries(uncheckpointed.EventID) != 0 || acked(uncheckpointed.EventID) != 0 {
			t.Fatal("unexpected uncheckpointed input recovery")
		}
		t.Log("OPEN CANDIDATE TRANSPORT GAP: publisher-accepted offline input after broker checkpoint is lost on SIGKILL with autosave disabled; no subscriber delivery/admission/ACK after restart and unique ordered barrier; not a production setting or default-policy claim")
	}
	if brokerMode == "checkpoint-kill" {
		t.Log("candidate-only broker SIGKILL after explicit checkpoint reloads retained session/offline queue; not uncheckpointed crash, power-loss/fsync/disk-full, replica or production settings acceptance")
	} else if brokerRestart {
		t.Log("candidate-only graceful broker restart reloads nonempty persistence file and retained subscriber session/offline queue; not abrupt broker kill, disk/fsync failure, replica or production configuration acceptance")
	}
	t.Log("candidate-only stable persistent subscriber: failed admission has no PUBACK, restart redelivers to one pending pair; commit-before-ACK process kill reconciles terminal exact admission without rearm; subscriber-process boundaries only, production unchanged")
}
