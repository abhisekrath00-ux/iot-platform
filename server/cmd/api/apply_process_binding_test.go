package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/applyadmission"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
	"github.com/eclipse/paho.mqtt.golang/packets"
	"github.com/jackc/pgx/v5"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Test executable entrypoint ONLY. No product startup flag enables the policy.
// Each child opens its own DB pool, independent binding, and one HTTP handler.
func TestApplyBoundProcessChild(t *testing.T) {
	if os.Getenv("TEST_BOUND_PROCESS_CHILD") != "1" {
		t.Skip("isolated child only")
	}
	st, err := store.Connect(t.Context(), os.Getenv("TEST_BOUND_PROCESS_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	f, err := applyadmission.OpenDeploymentFence(os.Getenv("TEST_BOUND_PROCESS_STORE"), os.Getenv("TEST_BOUND_PROCESS_BINDING"), "process-review")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{st: st, applySafetyCandidate: true, applyAttemptFence: f}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/gateways/{id}/config-push", s.pushGatewayConfig)
	mux.HandleFunc("GET /v1/gateways/{id}/config-push", s.listConfigPushes)
	tenant := os.Getenv("TEST_BOUND_PROCESS_TENANT")
	path := "/v1/gateways/" + tenant + "-gw/config-push"
	seen := call(mux, tenant, "admin", "GET", path, "")
	next := call(mux, tenant, "admin", "POST", path, `{}`)
	b, _ := json.Marshal(struct {
		Get  int
		Body string
		Post int
	}{seen.Code, seen.Body.String(), next.Code})
	fmt.Println("BOUND_PROCESS_RESULT:" + string(b))
}

// Real separate API test processes, retained directory/binding, PG schema dump
// and restore, and a loopback MQTT protocol peer. Not mTLS, image or native updater.
func TestIntegrationApplyBoundProcessArchiveRestore(t *testing.T) {
	if os.Getenv("TEST_PG_ARCHIVE_TOOLS") != "1" {
		t.Skip("matching explicit archive tools required")
	}
	s, _, tenant, _ := applyGuardFixture(t)
	if err := installApplyGuard(t, s); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	os.Chmod(base, 0700)
	directory := filepath.Join(base, "store")
	os.Mkdir(directory, 0700)
	binding := filepath.Join(base, "binding.json")
	if err := applyadmission.PrepareNewDeploymentBinding(directory, binding, "process-review"); err != nil {
		t.Fatal(err)
	}
	var schema string
	if err := s.st.Pool.QueryRow(t.Context(), "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(base, "before.dump")
	if out, err := exec.Command("pg_dump", "--dbname", os.Getenv("TEST_DATABASE_URL"), "--schema", schema, "--format=custom", "--file", archive).CombinedOutput(); err != nil {
		t.Fatalf("dump %v %s", err, out)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var connections, publications atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			connections.Add(1)
			func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(3 * time.Second))
				if _, e := packets.ReadPacket(c); e != nil {
					return
				}
				ack := packets.NewControlPacket(packets.Connack).(*packets.ConnackPacket)
				if ack.Write(c) != nil {
					return
				}
				p, e := packets.ReadPacket(c)
				if e != nil {
					return
				}
				publication, ok := p.(*packets.PublishPacket)
				if !ok {
					return
				}
				publications.Add(1)
				reply := packets.NewControlPacket(packets.Puback).(*packets.PubackPacket)
				reply.MessageID = publication.MessageID
				reply.Write(c)
			}()
		}
	}()
	defer func() { listener.Close(); <-done }()
	db, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	q := db.Query()
	q.Set("search_path", schema+",public")
	db.RawQuery = q.Encode()
	child := func() (int, string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestApplyBoundProcessChild$", "-test.v")
		cmd.Env = append(os.Environ(), "TEST_BOUND_PROCESS_CHILD=1", "TEST_BOUND_PROCESS_DSN="+db.String(), "TEST_BOUND_PROCESS_STORE="+directory, "TEST_BOUND_PROCESS_BINDING="+binding, "TEST_BOUND_PROCESS_TENANT="+tenant, "MQTT_TLS=false", "MQTT_HOST=127.0.0.1", fmt.Sprint("MQTT_PORT=", listener.Addr().(*net.TCPAddr).Port), "MQTT_CA_FILE=", "MQTT_CERT_FILE=", "MQTT_KEY_FILE=")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("child %v %s", err, out)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "BOUND_PROCESS_RESULT:") {
				var r struct {
					Get  int
					Body string
					Post int
				}
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "BOUND_PROCESS_RESULT:")), &r); err != nil {
					t.Fatal(err)
				}
				if r.Get != 200 {
					t.Fatal(r)
				}
				return r.Post, r.Body
			}
		}
		t.Fatalf("missing child result %s", out)
		return 0, ""
	}
	if code, _ := child(); code != 202 || publications.Load() != 1 {
		t.Fatal("first child publication", code, publications.Load())
	}
	if code, _ := child(); code != 409 || publications.Load() != 1 {
		t.Fatal("restart rearmed", code, publications.Load())
	}
	if _, err := s.st.Pool.Exec(t.Context(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("pg_restore", "--dbname", os.Getenv("TEST_DATABASE_URL"), "--no-owner", "--exit-on-error", "--single-transaction", archive).CombinedOutput(); err != nil {
		t.Fatalf("restore %v %s", err, out)
	}
	// Child uses fresh pool after actual archive restore, not cached statements.
	if code, body := child(); code != 409 || !strings.Contains(body, `"recovery_quarantined":true`) || publications.Load() != 1 || connections.Load() != 1 {
		t.Fatal("archive rearmed", code, body, publications.Load(), connections.Load())
	}
	tx, err := s.st.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err := applyadmission.Verify(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	t.Log("THREE_SEPARATE_API_TEST_PROCESSES_BOUND_STORE_REOPEN_SCHEMA_ARCHIVE_RESTORE_ONE_TCP_MQTT_PUBLICATION_POST409_QUARANTINE; not product entrypoint/image/mTLS/fullDB/physical execution")
}
