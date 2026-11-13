package main

import (
	"context"
	"errors"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
	"os"
	osexec "os/exec"
	"sync"
	"sync/atomic"
	"testing"
)

// These tests publish only to callbacks. No broker, actuator or notification
// service is reached. The claims and final states use real migrated PostgreSQL.
func TestIntegrationCommandDispatchClaim(t *testing.T) {
	if id := os.Getenv("HEXTHINGS_TEST_EXIT_CLAIM"); id != "" {
		st, err := store.Connect(context.Background(), os.Getenv("TEST_DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		s := &server{st: st}
		s.dispatchApproved(context.Background(), "itest-dispatch-claim", id, func(string, []byte) error { os.Exit(73); return nil })
		t.Fatal("child did not exit after committed claim")
	}
	s, _ := testServer(t)
	const tenant = "itest-dispatch-claim"
	seed(t, s, tenant)
	ctx := context.Background()
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := s.st.Pool.Exec(ctx, q, a...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`DELETE FROM commands WHERE tenant_id=$1`, tenant)
	exec(`INSERT INTO users(id,tenant_id,email,display_name,role)VALUES($1,$2,$3,'P','admin') ON CONFLICT DO NOTHING`, tenant+"-user", tenant, tenant+"@example.invalid")
	makeRow := func(id, status string, expired bool) {
		t.Helper()
		exec(`INSERT INTO commands(request_id,tenant_id,gateway_id,device_id,action,status,requested_by,approved_by,issued_at,expires_at)VALUES($1,$2,'fixture-gw','fixture-device','alarm.set',$3,$4,$4,now(),now()+$5::interval)`, id, tenant, status, tenant+"-user", map[bool]string{true: "-1 second", false: "1 minute"}[expired])
	}
	state := func(id string) string {
		t.Helper()
		var v string
		if err := s.st.Pool.QueryRow(ctx, `SELECT status FROM commands WHERE request_id=$1 AND tenant_id=$2`, id, tenant).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, status := range []string{"pending_approval", "sent", "acked", "failed", "outcome_unknown", "expired", "rejected"} {
		id := tenant + "-" + status
		makeRow(id, status, false)
		calls := 0
		next, _ := s.dispatchApproved(ctx, tenant, id, func(string, []byte) error { calls++; return nil })
		if calls != 0 || next != "not_dispatched" || state(id) != status {
			t.Fatalf("%s: calls=%d result=%s persisted=%s", status, calls, next, state(id))
		}
	}
	id := tenant + "-expired-approved"
	makeRow(id, "approved", true)
	calls := 0
	next, _ := s.dispatchApproved(ctx, tenant, id, func(string, []byte) error { calls++; return nil })
	if calls != 0 || next != "not_dispatched" || state(id) != "approved" {
		t.Fatal("expired approval published or changed")
	}
	id = tenant + "-wrong-tenant"
	makeRow(id, "approved", false)
	next, _ = s.dispatchApproved(ctx, "other-tenant", id, func(string, []byte) error { calls++; return nil })
	if calls != 0 || next != "not_dispatched" || state(id) != "approved" {
		t.Fatal("cross-tenant claim")
	}
	id = tenant + "-concurrent"
	makeRow(id, "approved", false)
	var published atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			s.dispatchApproved(ctx, tenant, id, func(string, []byte) error { published.Add(1); return nil })
		}()
	}
	close(start)
	wg.Wait()
	if published.Load() != 1 || state(id) != "sent" {
		t.Fatalf("concurrent publish=%d persisted=%s", published.Load(), state(id))
	}
	id = tenant + "-lost-publish-ack"
	makeRow(id, "approved", false)
	calls = 0
	next, _ = s.dispatchApproved(ctx, tenant, id, func(string, []byte) error { calls++; return errors.New("accepted but acknowledgement lost") })
	if calls != 1 || next != "outcome_unknown" || state(id) != "outcome_unknown" {
		t.Fatal("publish failure was not retained unknown")
	}
	s.dispatchApproved(ctx, tenant, id, func(string, []byte) error { calls++; return nil })
	if calls != 1 {
		t.Fatal("unknown outcome retried")
	}
	id = tenant + "-early-ack"
	makeRow(id, "approved", false)
	next, _ = s.dispatchApproved(ctx, tenant, id, func(string, []byte) error {
		exec(`UPDATE commands SET status='acked' WHERE request_id=$1 AND tenant_id=$2 AND status='outcome_unknown'`, id, tenant)
		return nil
	})
	if next != "acked" || state(id) != "acked" {
		t.Fatal("broker finalization overwrote early ACK")
	}
	id = tenant + "-lost-finalize"
	makeRow(id, "approved", false)
	lost, cancel := context.WithCancel(ctx)
	next, _ = s.dispatchApproved(lost, tenant, id, func(string, []byte) error { cancel(); return nil })
	if next != "outcome_unknown" || state(id) != "outcome_unknown" {
		t.Fatal("lost finalization did not retain unknown")
	}
	id = tenant + "-crash-after-claim"
	makeRow(id, "approved", false)
	child := osexec.Command(os.Args[0], "-test.run=^TestIntegrationCommandDispatchClaim$", "-test.count=1")
	child.Env = append(os.Environ(), "HEXTHINGS_TEST_EXIT_CLAIM="+id)
	output, childErr := child.CombinedOutput()
	var exited *osexec.ExitError
	if !errors.As(childErr, &exited) || exited.ExitCode() != 73 {
		t.Fatalf("child exit=%v output=%s", childErr, output)
	}

	if state(id) != "outcome_unknown" {
		t.Fatal("claim did not survive callback crash")
	}
	calls = 0
	s.dispatchApproved(ctx, tenant, id, func(string, []byte) error { calls++; return nil })
	if calls != 0 {
		t.Fatal("crashed claim retried")
	}
	id = tenant + "-cancel-before-claim"
	makeRow(id, "approved", false)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	calls = 0
	s.dispatchApproved(cancelled, tenant, id, func(string, []byte) error { calls++; return nil })
	if calls != 0 || state(id) != "approved" {
		t.Fatal("cancelled context published")
	}
	// A catalog error is not proof that no claim committed. Never publish,
	// but return uncertainty instead of the no-matching-row result.
	s.st.Close()
	calls = 0
	next, _ = s.dispatchApproved(ctx, tenant, id, func(string, []byte) error { calls++; return nil })
	if calls != 0 || next != "outcome_unknown" {
		t.Fatal("catalog uncertainty misreported")
	}

}
