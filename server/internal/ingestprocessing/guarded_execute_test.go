package ingestprocessing

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func guardedExecutionConfig(t *testing.T, x Engine, f httpMTLS) GuardedExecutionConfig {
	t.Helper()
	return GuardedExecutionConfig{Enabled: true, Engine: x, Routes: httpConfig(t, f, x).Routes, MigrationDirectory: "../../migrations"}
}
func mustGuardedExecutor(t *testing.T, x Engine, f httpMTLS) *GuardedExecutor {
	t.Helper()
	g, err := NewGuardedExecutor(t.Context(), guardedExecutionConfig(t, x, f))
	if err != nil {
		t.Fatal(err)
	}
	return g
}
func quarantineThroughRuntime(t *testing.T, f httpMTLS, endpoint string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), "POST", endpoint+"/staged/processing-v1/quarantine-gateway", strings.NewReader(quarantineBody(t, f)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+quarantineToken(t, "a", "u", "session", "admin"))
	resp, err := quarantineServerClient(t, f).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
}
func TestGuardedExecutionDefaultOffPreflightAndFrozenRoutes(t *testing.T) {
	t.Parallel()
	if g, err := NewGuardedExecutor(t.Context(), GuardedExecutionConfig{}); g != nil || !errors.Is(err, ErrDisabled) {
		t.Fatal(g, err)
	}
	var none *GuardedExecutor
	if err := none.ExecuteOnce(t.Context(), "a", "input", time.Now(), nil); !errors.Is(err, ErrGuardedExecution) {
		t.Fatal(err)
	}
	x, e := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	c := guardedExecutionConfig(t, x, f)
	for _, mode := range []string{"nil-context", "engine-off", "no-pool", "no-routes", "bad-topic", "zero-leaf", "duplicate-topic", "duplicate-leaf", "wrong-binding"} {
		t.Run(mode, func(t *testing.T) {
			cfg := c
			cfg.Routes = append([]CertificateRoute(nil), c.Routes...)
			ctx := t.Context()
			switch mode {
			case "nil-context":
				ctx = nil
			case "engine-off":
				cfg.Engine.Enabled = false
			case "no-pool":
				cfg.Engine.Pool = nil
			case "no-routes":
				cfg.Routes = nil
			case "bad-topic":
				cfg.Routes[0].Topic = "bad"
			case "zero-leaf":
				cfg.Routes[0].LeafSHA256 = [32]byte{}
			case "duplicate-topic":
				cfg.Routes = append(cfg.Routes, cfg.Routes[0])
			case "duplicate-leaf":
				cfg.Routes = append(cfg.Routes, CertificateRoute{LeafSHA256: cfg.Routes[0].LeafSHA256, Topic: "t/a/g/other/telemetry"})
			case "wrong-binding":
				cfg.Routes[0].LeafSHA256 = [32]byte{9}
			}
			if g, err := NewGuardedExecutor(ctx, cfg); g != nil || err == nil {
				t.Fatal(g, err)
			}
		})
	}
	g, err := NewGuardedExecutor(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	c.Routes[0].LeafSHA256 = [32]byte{7}
	c.Routes[0].Topic = "t/b/g/g/telemetry"
	mustAdmit(t, x, e)
	calls := 0
	if err = g.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, syntheticSender(func(context.Context, Envelope, Intent) error { calls++; return nil })); err != nil || calls != 2 {
		t.Fatal(err, calls)
	}
	assertState(t, x, e, "completed")
	old, _ := fixture(t)
	if g, err = NewGuardedExecutor(t.Context(), guardedExecutionConfig(t, old, f)); g != nil || err == nil {
		t.Fatal("exact94 accepted", g, err)
	}
	t.Log("GUARDED_EXECUTION_DEFAULT_OFF_EXACT95_FROZEN_SINGLE_GATEWAY_ROUTE_OK")
}
func TestGuardedExecutionActualRuntimeQuarantineBeforeAndBetweenEffects(t *testing.T) {
	t.Parallel()
	for _, between := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-claim", true: "between-effects"}[between], func(t *testing.T) {
			x, e := denialFixture(t)
			f := httpTLSFixture(t)
			bindGatewayLeaf(t, x, f)
			seedQuarantineSession(t, x)
			runtime, err := NewStagedGatewayRuntime(t.Context(), runtimeConfig(t, x, f))
			if err != nil {
				t.Fatal(err)
			}
			a, m, cancel, done := startGatewayRuntime(t, runtime)
			defer cancel()
			g := mustGuardedExecutor(t, x, f)
			if code, _ := durableRequest(t, f, a, e); code != 200 {
				t.Fatal(code)
			}
			if !between {
				quarantineThroughRuntime(t, f, m)
			}
			calls := 0
			s := syntheticSender(func(ctx context.Context, got Envelope, i Intent) error {
				calls++
				if got != e || i.Sequence != 0 {
					t.Fatal(got, i)
				}
				if between {
					quarantineThroughRuntime(t, f, m)
				}
				return nil
			})
			err = g.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, s)
			if between {
				if !errors.Is(err, ErrUnknown) || calls != 1 {
					t.Fatal(err, calls)
				}
				assertState(t, x, e, "outcome_unknown")
				historyCount(t, x, 1, "processing_outcome_unknown")
				if err = g.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, s); !errors.Is(err, ErrState) {
					t.Fatal(err)
				}
			} else {
				if !errors.Is(err, ErrGatewayAuthority) || calls != 0 {
					t.Fatal(err, calls)
				}
				assertState(t, x, e, "pending")
				historyCount(t, x, 0, "processing_started")
			}
			if code, _ := durableRequest(t, f, a, e); code != 503 {
				t.Fatal(code)
			}
			quarantineState(t, x, "revoked", 1)
			counts(t, x.Pool, e.EventID, 1)
			cancel()
			select {
			case err = <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("runtime drain")
			}
			if err = runtime.WaitDrained(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Log("GUARDED_EXECUTION_ACTUAL_COMPOSED_MTLS_ADMIT_HTTPS_QUARANTINE_PENDING_NO_IO_AND_BETWEEN_UNKNOWN_NO_RETRY_OK")
}
func TestGuardedExecutionCurrentAuthorityAndDenialRefuseClaim(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"denied", "fingerprint", "status", "kind", "auth-mode", "tenant", "missing-epoch", "unmapped"} {
		t.Run(mode, func(t *testing.T) {
			x, e := denialFixture(t)
			f := httpTLSFixture(t)
			bindGatewayLeaf(t, x, f)
			g := mustGuardedExecutor(t, x, f)
			mustAdmit(t, x, e)
			switch mode {
			case "denied":
				if err := x.DenyLeaf(t.Context(), sha256.Sum256(f.client.Certificate[0])); err != nil {
					t.Fatal(err)
				}
			case "fingerprint":
				exec(t, x.Pool, "UPDATE gateways SET cert_fingerprint=repeat('0',64) WHERE id='g'")
			case "status":
				exec(t, x.Pool, "UPDATE gateways SET status='revoked' WHERE id='g'")
			case "kind":
				exec(t, x.Pool, "UPDATE gateways SET kind='direct' WHERE id='g'")
			case "auth-mode":
				exec(t, x.Pool, "UPDATE gateways SET auth_mode='password' WHERE id='g'")
			case "tenant":
				exec(t, x.Pool, "UPDATE gateways SET tenant_id='b' WHERE id='g'")
			case "missing-epoch":
				exec(t, x.Pool, "DELETE FROM admission_denial_epoch_v1")
			case "unmapped":
				g.engine.executionRoutes = map[string][32]byte{}
			}
			var calls atomic.Int32
			err := g.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, syntheticSender(func(context.Context, Envelope, Intent) error { calls.Add(1); return nil }))
			if err == nil || calls.Load() != 0 {
				t.Fatal(err, calls.Load())
			}
			assertState(t, x, e, "pending")
			historyCount(t, x, 0, "processing_started")
		})
	}
}
func TestGuardedExecutionLegacyPendingAfterQuarantineStillBypasses(t *testing.T) {
	// Reproduces and preserves the explicit boundary: callers using legacy Engine
	// are NOT protected. Do not infer full product revocation from the new API.
	t.Parallel()
	x, e := denialFixture(t)
	mustAdmit(t, x, e)
	exec(t, x.Pool, "UPDATE gateways SET status='revoked' WHERE id='g'")
	calls := 0
	err := x.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, syntheticSender(func(context.Context, Envelope, Intent) error { calls++; return nil }))
	if err != nil || calls != 2 {
		t.Fatal(err, calls)
	}
	t.Log("LEGACY_EXECUTION_PENDING_REVOKED_GATEWAY_BYPASS_REPRODUCED_NOT_PRODUCTION_FIXED")
}

func TestGuardedExecutionFourActualSendersCancelDoesNotFakeDrain(t *testing.T) {
	t.Parallel()
	x, e := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	g := mustGuardedExecutor(t, x, f)
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	defer close(release)
	results := make(chan error, 4)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	for n := 0; n < 4; n++ {
		v := e
		v.EventID = fmt.Sprintf("concurrent-%d", n)
		mustAdmit(t, x, v)
	}
	// Distinct serializable claims are entered in a fixed sequence, not retried.
	// Concurrent DB claims can legitimately serialization-fail before provider I/O.
	for n := 0; n < 4; n++ {
		v := e
		v.EventID = fmt.Sprintf("concurrent-%d", n)
		go func() {
			results <- g.ExecuteOnce(ctx, v.TenantID, v.EventID, v.ObservedAt, syntheticSender(func(context.Context, Envelope, Intent) error { entered <- struct{}{}; <-release; return nil }))
		}()
		select {
		case <-entered:
		case err := <-results:
			t.Fatal("claim failed before sender", err)
		case <-time.After(5 * time.Second):
			t.Fatal("missing actual sender")
		}
	}
	v := e
	v.EventID = "fifth"
	mustAdmit(t, x, v)
	noSender := syntheticSender(func(context.Context, Envelope, Intent) error { t.Error("busy entered provider"); return nil })
	if err := g.ExecuteOnce(t.Context(), v.TenantID, v.EventID, v.ObservedAt, noSender); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-results:
		t.Fatal("cancellation pretended noncooperative sender returned", err)
	default:
	}
	if err := g.ExecuteOnce(t.Context(), v.TenantID, v.EventID, v.ObservedAt, noSender); !errors.Is(err, ErrBusy) {
		t.Fatal("slot released before actual return", err)
	}
	assertState(t, x, v, "pending")
	// Release now, but retain deferred cleanup without double-close.
	for n := 0; n < 4; n++ {
		release <- struct{}{}
	}
	for n := 0; n < 4; n++ {
		select {
		case err := <-results:
			if !errors.Is(err, ErrUnknown) {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("actual sender drain")
		}
	}
	historyCount(t, x, 4, "processing_outcome_unknown")
	historyCount(t, x, 0, "processing_completed")
	t.Log("GUARDED_EXECUTION_FOUR_ACTUAL_SENDERS_FIFTH_BUSY_CANCEL_HOLDS_SLOTS_UNTIL_ACTUAL_RETURN_NO_RETRY_OK")
}
func TestGuardedExecutionActualGatewayRowWaitCancelNoClaimOrIO(t *testing.T) {
	t.Parallel()
	x, e := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	g := mustGuardedExecutor(t, x, f)
	mustAdmit(t, x, e)
	tx, err := x.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	if _, err = tx.Exec(t.Context(), "SELECT id FROM gateways WHERE id='g' FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	var calls atomic.Int32
	go func() {
		result <- g.ExecuteOnce(ctx, e.TenantID, e.EventID, e.ObservedAt, syntheticSender(func(context.Context, Envelope, Intent) error { calls.Add(1); return nil }))
	}()
	waitQuarantineSQL(t, x, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'SELECT status,kind,auth_mode%'")
	cancel()
	select {
	case err = <-result:
		if err == nil {
			t.Fatal("canceled waiter succeeded")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("canceled DB call did not return")
	}
	rollback(tx)
	assertState(t, x, e, "pending")
	historyCount(t, x, 0, "processing_started")
	if calls.Load() != 0 {
		t.Fatal(calls.Load())
	}
	t.Log("GUARDED_EXECUTION_ACTUAL_GATEWAY_LOCK_WAITER_CANCEL_ZERO_CLAIM_HISTORY_PROVIDER_CALLS_OK")
}
