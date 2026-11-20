package ingestprocessing

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func runtimeExecutionConfig(t *testing.T, x Engine, f httpMTLS) StagedGatewayRuntimeConfig {
	t.Helper()
	c := runtimeConfig(t, x, f)
	ec := guardedExecutionConfig(t, x, f)
	c.Execution = &ec
	return c
}
func TestRuntimeExecutionExplicitMatchingConfiguration(t *testing.T) {
	t.Parallel()
	x, e := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	c := runtimeExecutionConfig(t, x, f)
	for _, mode := range []string{"off", "engine-off", "pool", "dir", "missing-route", "extra-route", "wrong-leaf", "wrong-topic"} {
		t.Run(mode, func(t *testing.T) {
			cfg := c
			ec := *c.Execution
			ec.Routes = append([]CertificateRoute(nil), ec.Routes...)
			cfg.Execution = &ec
			switch mode {
			case "off":
				ec.Enabled = false
			case "engine-off":
				ec.Engine.Enabled = false
			case "pool":
				ec.Engine.Pool = &pgxpool.Pool{}
			case "dir":
				ec.MigrationDirectory = "other"
			case "missing-route":
				ec.Routes = nil
			case "extra-route":
				ec.Routes = append(ec.Routes, ec.Routes[0])
			case "wrong-leaf":
				ec.Routes[0].LeafSHA256 = [32]byte{9}
			case "wrong-topic":
				ec.Routes[0].Topic = "t/b/g/g/telemetry"
			}
			if s, err := NewStagedGatewayRuntime(t.Context(), cfg); s != nil || err == nil {
				t.Fatal(s, err)
			}
		})
	}
	s, err := NewStagedGatewayRuntime(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	sender := syntheticSender(func(context.Context, Envelope, Intent) error { t.Error("unstarted execution"); return nil })
	if err = s.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, sender); !errors.Is(err, ErrStagedGatewayRuntime) {
		t.Fatal(err)
	}
	// Source slice edits cannot widen either frozen admission or execution routing.
	c.Execution.Routes[0].LeafSHA256 = [32]byte{8}
	c.Execution.Routes[0].Topic = "t/b/g/g/telemetry"
	a, _, cancel, done := startGatewayRuntime(t, s)
	if code, _ := durableRequest(t, f, a, e); code != 200 {
		t.Fatal(code)
	}
	assertState(t, x, e, "pending")
	historyCount(t, x, 0, "processing_started")
	calls := 0
	if err = s.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, syntheticSender(func(ctx context.Context, got Envelope, i Intent) error { calls++; return nil })); err != nil || calls != 2 {
		t.Fatal(err, calls)
	}
	assertState(t, x, e, "completed")
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if err = s.WaitDrained(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = s.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, sender); !errors.Is(err, ErrStagedGatewayRuntime) {
		t.Fatal(err)
	}
	// Historical nil execution config stays opt-out even during live Serve.
	old, err := NewStagedGatewayRuntime(t.Context(), runtimeConfig(t, x, f))
	if err != nil {
		t.Fatal(err)
	}
	a, _, cancel, done = startGatewayRuntime(t, old)
	defer cancel()
	if code, _ := durableRequest(t, f, a, e); code != 200 {
		t.Fatal(code)
	}
	if err = old.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, sender); !errors.Is(err, ErrStagedGatewayRuntime) {
		t.Fatal(err)
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if err = old.WaitDrained(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Log("COMPOSED_EXECUTION_EXPLICIT_SAME_POOL_ROUTESET_FROZEN_BEFORE_AFTER_REFUSAL_NO_AUTOMATIC_WORK_OK")
}
func TestRuntimeExecutionActualQuarantineBetweenOccurrences(t *testing.T) {
	t.Parallel()
	x, e := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	seedQuarantineSession(t, x)
	s, err := NewStagedGatewayRuntime(t.Context(), runtimeExecutionConfig(t, x, f))
	if err != nil {
		t.Fatal(err)
	}
	a, m, cancel, done := startGatewayRuntime(t, s)
	defer cancel()
	if code, _ := durableRequest(t, f, a, e); code != 200 {
		t.Fatal(code)
	}
	calls := 0
	err = s.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, syntheticSender(func(context.Context, Envelope, Intent) error { calls++; quarantineThroughRuntime(t, f, m); return nil }))
	if !errors.Is(err, ErrUnknown) || calls != 1 {
		t.Fatal(err, calls)
	}
	assertState(t, x, e, "outcome_unknown")
	historyCount(t, x, 1, "processing_outcome_unknown")
	quarantineState(t, x, "revoked", 1)
	if err = s.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, nil); !errors.Is(err, ErrState) {
		t.Fatal(err)
	}
	if code, _ := durableRequest(t, f, a, e); code != 503 {
		t.Fatal(code)
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if err = s.WaitDrained(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Log("COMPOSED_EXECUTION_MTLS_ADMIT_EXPLICIT_BATCH_ADMIN_HTTPS_QUARANTINE_ONE_SYNTHETIC_EFFECT_UNKNOWN_NO_RETRY_OK")
}
func TestRuntimeExecutionFailedTransportOrCancelIsNotActualSenderDrain(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"listener-fails", "parent-cancel", "caller-cancel"} {
		t.Run(mode, func(t *testing.T) {
			x, e := denialFixture(t)
			f := httpTLSFixture(t)
			bindGatewayLeaf(t, x, f)
			s, err := NewStagedGatewayRuntime(t.Context(), runtimeExecutionConfig(t, x, f))
			if err != nil {
				t.Fatal(err)
			}
			a, m := runtimeListener(t), runtimeListener(t)
			life, stop := context.WithCancel(t.Context())
			defer stop()
			served := make(chan error, 1)
			go func() { served <- s.Serve(life, a, m) }()
			if code, _ := durableRequest(t, f, "https://"+a.Addr().String(), e); code != 200 {
				t.Fatal(code)
			}
			entered, release, canceled := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			executed := make(chan error, 1)
			go func() {
				executed <- s.ExecuteOnce(ctx, e.TenantID, e.EventID, e.ObservedAt, syntheticSender(func(ctx context.Context, got Envelope, i Intent) error {
					if calls.Add(1) == 1 {
						close(entered)
					}
					<-ctx.Done()
					close(canceled)
					<-release
					return nil
				}))
			}()
			select {
			case <-entered:
			case <-time.After(4 * time.Second):
				t.Fatal("sender did not enter")
			}
			var released bool
			defer func() {
				if !released {
					close(release)
				}
			}()
			switch mode {
			case "listener-fails":
				a.Close()
			case "parent-cancel":
				stop()
			case "caller-cancel":
				cancel()
			}
			select {
			case <-canceled:
			case <-time.After(4 * time.Second):
				t.Fatal("sender context not canceled")
			}
			select {
			case err = <-executed:
				t.Fatal("pretended sender drain", err)
			default:
			}
			if mode == "caller-cancel" {
				// Caller cancellation does not kill the two separately live transports.
				if code, _ := durableRequest(t, f, "https://"+a.Addr().String(), e); code != 200 {
					t.Fatal("caller stopped transport", code)
				}
				stop()
			}
			select {
			case err = <-served:
				if !errors.Is(err, ErrStagedGatewayDrain) {
					t.Fatal(err)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("transport stop hung on sender")
			}
			wait, done := context.WithTimeout(t.Context(), 20*time.Millisecond)
			if err = s.WaitDrained(wait); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("false execution drain", err)
			}
			done()
			if err = s.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, nil); !errors.Is(err, ErrStagedGatewayRuntime) {
				t.Fatal(err)
			}
			assertState(t, x, e, "executing")
			historyCount(t, x, 1, "processing_started")
			close(release)
			released = true
			select {
			case err = <-executed:
				if !errors.Is(err, ErrUnknown) {
					t.Fatal(err)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("sender actual return")
			}
			wait, done = context.WithTimeout(t.Context(), time.Second)
			defer done()
			if err = s.WaitDrained(wait); err != nil {
				t.Fatal(err)
			}
			assertState(t, x, e, "outcome_unknown")
			historyCount(t, x, 1, "processing_outcome_unknown")
			if calls.Load() != 1 {
				t.Fatal("retried after cancel", calls.Load())
			}
			if _, err = net.DialTimeout("tcp", a.Addr().String(), time.Second); err == nil {
				t.Fatal("admission reopened")
			}
			if _, err = net.DialTimeout("tcp", m.Addr().String(), time.Second); err == nil {
				t.Fatal("management reopened")
			}
		})
	}
	t.Log("COMPOSED_EXECUTION_LISTENER_PARENT_CALLER_CANCEL_ACTUAL_SENDER_RETURN_REQUIRED_BOTH_TRANSPORTS_CLOSED_NO_RETRY_OK")
}

func TestRuntimeExecutionActualClaimDBWaitCancelledWithRuntime(t *testing.T) {
	t.Parallel()
	x, e := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	s, err := NewStagedGatewayRuntime(t.Context(), runtimeExecutionConfig(t, x, f))
	if err != nil {
		t.Fatal(err)
	}
	a, _, cancel, done := startGatewayRuntime(t, s)
	defer cancel()
	if code, _ := durableRequest(t, f, a, e); code != 200 {
		t.Fatal(code)
	}
	blocker, err := x.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(blocker)
	if _, err = blocker.Exec(t.Context(), "SELECT id FROM gateways WHERE id='g' FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	var calls atomic.Int32
	go func() {
		result <- s.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, syntheticSender(func(context.Context, Envelope, Intent) error { calls.Add(1); return nil }))
	}()
	waitQuarantineSQL(t, x, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'SELECT status,kind,auth_mode%'")
	cancel()
	select {
	case err = <-done:
		if err != nil && !errors.Is(err, ErrStagedGatewayDrain) {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("runtime transport exit")
	}
	wait, stop := context.WithTimeout(t.Context(), 2*time.Second)
	defer stop()
	if err = s.WaitDrained(wait); err != nil {
		t.Fatal("actual DB waiter did not drain", err)
	}
	select {
	case err = <-result:
		if err == nil {
			t.Fatal("DB waiter succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("execution return missing")
	}
	rollback(blocker)
	assertState(t, x, e, "pending")
	historyCount(t, x, 0, "processing_started")
	if calls.Load() != 0 {
		t.Fatal(calls.Load())
	}
	t.Log("COMPOSED_EXECUTION_ACTUAL_PG_CLAIM_WAIT_RUNTIME_CANCEL_ACTUAL_DRAIN_ZERO_PROVIDER_HISTORY_WRITES_OK")
}

func TestRuntimeExecutionPreservesChildDrainWarningAfterCountChanges(t *testing.T) {
	for _, err := range []error{ErrAdmissionDrain, ErrQuarantineDrain, fmt.Errorf("child result: %w", ErrAdmissionDrain), fmt.Errorf("child result: %w", ErrQuarantineDrain)} {
		if !stagedChildUndrained(err) {
			t.Fatal("lost child drain warning", err)
		}
	}
	for _, err := range []error{nil, context.Canceled, ErrAdmissionServer, ErrQuarantineServer} {
		if stagedChildUndrained(err) {
			t.Fatal("invented drain warning", err)
		}
	}
	t.Log("COMPOSED_CHILD_EXPLICIT_DRAIN_SENTINELS_PRESERVED_AFTER_LATER_ACTIVE_COUNT_ZERO_OK")
}
