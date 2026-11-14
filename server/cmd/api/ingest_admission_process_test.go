package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/ingestjournal"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
)

// Each helper is a separate OS process running the real candidate. No production
// hook changes Admit. The proxy is plaintext and only connects to the disposable
// TEST_DATABASE_URL fixture; it is not TLS, failover or broker durability proof.
func TestAdmissionCandidateProcessHelper(t *testing.T) {
	if os.Getenv("HEX_ADMISSION_CHILD") != "1" {
		t.Skip("subprocess helper")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := store.Connect(ctx, os.Getenv("HEX_ADMISSION_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var e ingestjournal.Envelope
	if err := json.Unmarshal([]byte(os.Getenv("HEX_ADMISSION_EVENT")), &e); err != nil {
		t.Fatal(err)
	}
	j := ingestjournal.Journal{Pool: st.Pool}
	var fresh bool
	if os.Getenv("HEX_ADMISSION_ACTION") == "claim" {
		_, err = j.Claim(ctx, e.TenantID, e.EventID, e.ObservedAt)
		fresh = err == nil
	} else {
		fresh, err = j.Admit(ctx, e)
	}
	text := ""
	if err != nil {
		text = err.Error()
	}
	b, _ := json.Marshal(struct {
		Fresh bool
		Error string
	}{fresh, text})
	fmt.Printf("ADMISSION_RESULT:%s\n", b)
}

type admissionWireGate struct {
	url     string
	reached chan struct{}
	release chan struct{}
	stop    func()
}

// Stop at the COMMIT request or at PostgreSQL's actual COMMIT CommandComplete.
// The latter response is deliberately withheld from the client. All other
// messages are forwarded normally, including startup and transaction writes.
func newAdmissionWireGate(t *testing.T, rawURL, mode string) admissionWireGate {
	t.Helper()
	cfg, err := pgconn.ParseConfig(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TLSConfig != nil {
		t.Skip("wire fault fixture requires plaintext disposable PostgreSQL")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("wire fixture requires PostgreSQL URL", err)
	}
	u.Host = ln.Addr().String()
	q := u.Query()
	q.Del("host")
	q.Del("port")
	q.Set("sslmode", "disable")
	q.Set("pool_max_conns", "1")
	u.RawQuery = q.Encode()
	reached, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done); close(release); _ = ln.Close() }) }
	t.Cleanup(stop)
	go func() {
		client, err := ln.Accept()
		if err != nil {
			return
		}
		defer client.Close()
		var target string
		if strings.HasPrefix(cfg.Host, "/") {
			target = fmt.Sprintf("%s/.s.PGSQL.%d", cfg.Host, cfg.Port)
		} else {
			target = net.JoinHostPort(cfg.Host, fmt.Sprint(cfg.Port))
		}
		network := "tcp"
		if strings.HasPrefix(cfg.Host, "/") {
			network = "unix"
		}
		upstream, err := net.DialTimeout(network, target, 5*time.Second)
		if err != nil {
			return
		}
		defer upstream.Close()
		go func() { <-done; _ = client.Close(); _ = upstream.Close() }()
		backend := pgproto3.NewBackend(client, client)
		frontend := pgproto3.NewFrontend(upstream, upstream)
		startup, err := backend.ReceiveStartupMessage()
		if err != nil {
			return
		}
		frontend.Send(startup)
		if frontend.Flush() != nil {
			return
		}
		go func() {
			defer stop()
			for {
				msg, err := backend.Receive()
				if err != nil {
					return
				}
				commit := false
				switch m := msg.(type) {
				case *pgproto3.Query:
					commit = strings.EqualFold(strings.TrimSpace(m.String), "commit")
				case *pgproto3.Parse:
					commit = strings.EqualFold(strings.TrimSpace(m.Query), "commit")
				}
				if mode == "before-commit" && commit {
					close(reached)
					<-release
					return
				}
				frontend.Send(msg)
				if frontend.Flush() != nil {
					return
				}
			}
		}()
		defer stop()
		for {
			msg, err := frontend.Receive()
			if err != nil {
				return
			}
			if m, ok := msg.(*pgproto3.CommandComplete); ok && string(m.CommandTag) == "COMMIT" && mode != "before-commit" {
				close(reached)
				if mode == "after-commit-kill" {
					<-release
				}
				return // drop committed response and close both directions
			}
			backend.Send(msg)
			if backend.Flush() != nil {
				return
			}
		}
	}()
	return admissionWireGate{u.String(), reached, release, stop}
}

func TestIntegrationAdmissionCandidateProcessCrash(t *testing.T) {
	s, _ := testServer(t)
	ctx := t.Context()
	const tenant = "itest-inbox-process"
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
		if _, err := s.st.Pool.Exec(c, `DROP TABLE IF EXISTS ingest_admission_candidate`); err != nil {
			t.Error(err)
		}
	})
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := func(t *testing.T, e ingestjournal.Envelope, dbURL string) (*exec.Cmd, *bytes.Buffer) {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestAdmissionCandidateProcessHelper$", "-test.timeout=30s")
		cmd.Env = append(os.Environ(), "HEX_ADMISSION_CHILD=1", "HEX_ADMISSION_URL="+dbURL, "HEX_ADMISSION_EVENT="+string(b))
		out := &bytes.Buffer{}
		cmd.Stdout = out
		cmd.Stderr = out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return cmd, out
	}
	readResult := func(t *testing.T, cmd *exec.Cmd, out *bytes.Buffer) (bool, string) {
		t.Helper()
		if err := cmd.Wait(); err != nil {
			t.Fatalf("helper: %v %s", err, out)
		}
		for _, line := range strings.Split(out.String(), "\n") {
			if strings.HasPrefix(line, "ADMISSION_RESULT:") {
				var result struct {
					Fresh bool
					Error string
				}
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "ADMISSION_RESULT:")), &result); err != nil {
					t.Fatal(err)
				}
				return result.Fresh, result.Error
			}
		}
		t.Fatalf("missing child result: %s", out)
		return false, ""
	}
	pair := func(t *testing.T, e ingestjournal.Envelope, want int, state string) {
		t.Helper()
		var raw, pending int
		if err := s.st.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM telemetry WHERE event_id=$1 AND observed_at=$2),(SELECT count(*) FROM ingest_admission_candidate WHERE event_id=$1 AND observed_at=$2 AND state=$3)`, e.EventID, e.ObservedAt, state).Scan(&raw, &pending); err != nil || raw != want || pending != want {
			t.Fatalf("atomic pair got %d/%d want%d: %v", raw, pending, want, err)
		}
	}
	for _, mode := range []string{"before-commit", "after-commit-kill", "commit-response-lost"} {
		t.Run(mode, func(t *testing.T) {
			e := ingestjournal.Envelope{EventID: "itest-process-" + mode, TenantID: tenant, GatewayID: tenant + "-gw", DeviceID: tenant + "-dev", PointID: "temp", ObservedAt: time.Now().UTC().Truncate(time.Microsecond), Value: 42, Unit: "C", Quality: "measured", SchemaVersion: 1}
			gate := newAdmissionWireGate(t, os.Getenv("TEST_DATABASE_URL"), mode)
			cmd, out := child(t, e, gate.url)
			select {
			case <-gate.reached:
			case <-time.After(15 * time.Second):
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				t.Fatalf("wire boundary not reached: %s", out)
			}
			if mode == "commit-response-lost" {
				fresh, msg := readResult(t, cmd, out)
				if fresh || !strings.Contains(msg, "admission commit outcome requires reconciliation") {
					t.Fatalf("uncertainty not surfaced: fresh%v err%s", fresh, msg)
				}
			} else {
				if err := cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				if err := cmd.Wait(); err == nil {
					t.Fatal("child unexpectedly exited successfully")
				}
			}
			gate.stop()
			// A direct independent connection must observe transaction cleanup before
			// retry. The precommit proxy closes its upstream only after child death.
			lockConn, err := s.st.Pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer lockConn.Release()
			deadline := time.Now().Add(5 * time.Second)
			for {
				var got bool
				err := lockConn.QueryRow(ctx, `SELECT pg_try_advisory_lock(731492068213)`).Scan(&got)
				if err != nil {
					t.Fatal(err)
				}
				if got {
					_, err = lockConn.Exec(ctx, `SELECT pg_advisory_unlock(731492068213)`)
					if err != nil {
						t.Fatal(err)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("transaction lock survived disconnect")
				}
				time.Sleep(10 * time.Millisecond)
			}
			want := 1
			if mode == "before-commit" {
				want = 0
			}
			pair(t, e, want, "pending")
			retry, retryOut := child(t, e, os.Getenv("TEST_DATABASE_URL"))
			fresh, msg := readResult(t, retry, retryOut)
			if fresh != (want == 0) || msg != "" {
				t.Fatalf("independent retry fresh%v err%s", fresh, msg)
			}
			pair(t, e, 1, "pending")
			replay, replayOut := child(t, e, os.Getenv("TEST_DATABASE_URL"))
			fresh, msg = readResult(t, replay, replayOut)
			if fresh || msg != "" {
				t.Fatalf("independent replay fresh%v err%s", fresh, msg)
			}
			pair(t, e, 1, "pending")
			t.Log("actual child process boundary and independent retry preserve one pending envelope; no worker/effect ran")
		})
	}
	t.Run("claim-committed-child-killed", func(t *testing.T) {
		e := ingestjournal.Envelope{EventID: "itest-process-claim", TenantID: tenant, GatewayID: tenant + "-gw", DeviceID: tenant + "-dev", PointID: "temp", ObservedAt: time.Now().UTC().Truncate(time.Microsecond), Value: 42, Unit: "C", Quality: "measured", SchemaVersion: 1}
		j := ingestjournal.Journal{Pool: s.st.Pool}
		if fresh, err := j.Admit(ctx, e); err != nil || !fresh {
			t.Fatal(fresh, err)
		}
		t.Setenv("HEX_ADMISSION_ACTION", "claim")
		gate := newAdmissionWireGate(t, os.Getenv("TEST_DATABASE_URL"), "after-commit-kill")
		cmd, out := child(t, e, gate.url)
		select {
		case <-gate.reached:
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("claim commit not reached: %s", out)
		}
		if err := cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Wait(); err == nil {
			t.Fatal("claim helper unexpectedly successful")
		}
		gate.stop()
		jr, err := store.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		defer jr.Close()
		recovered := ingestjournal.Journal{Pool: jr.Pool}
		w, err := recovered.ReadWork(ctx, tenant, e.EventID, e.ObservedAt)
		if err != nil || w.State != "executing" || w.AttemptID == "" {
			t.Fatal("lost-response claim evidence", w, err)
		}
		if _, err = recovered.Claim(ctx, tenant, e.EventID, e.ObservedAt); !errors.Is(err, ingestjournal.ErrState) {
			t.Fatal("interrupted attempt taken over", err)
		}
		if err = recovered.MarkUnknown(ctx, tenant, e.EventID, e.ObservedAt, w.AttemptID); err != nil {
			t.Fatal(err)
		}
		if err = recovered.Complete(ctx, tenant, e.EventID, e.ObservedAt, w.AttemptID); !errors.Is(err, ingestjournal.ErrState) {
			t.Fatal("late completion changed quarantine", err)
		}
		if _, err = recovered.Claim(ctx, tenant, e.EventID, e.ObservedAt); !errors.Is(err, ingestjournal.ErrState) {
			t.Fatal("unknown attempt requeued", err)
		}
		if fresh, err := recovered.Admit(ctx, e); fresh || err != nil {
			t.Fatal("unknown replay rearmed", fresh, err)
		}
		pair(t, e, 1, "outcome_unknown")
		final, err := recovered.ReadWork(ctx, tenant, e.EventID, e.ObservedAt)
		if err != nil || final.State != "outcome_unknown" || final.AttemptID != w.AttemptID {
			t.Fatal("quarantine not durable", final, err)
		}
		t.Log("actual process killed after claim commit but before response: restart keeps executing, explicit matching-token quarantine is terminal; no automatic takeover/effect")
	})

	var ledger int
	if err := s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&ledger); err != nil || ledger != 93 {
		t.Fatal("candidate changed migration ledger", ledger, err)
	}
}
