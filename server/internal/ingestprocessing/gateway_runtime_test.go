package ingestprocessing

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/authorityschema"
	"github.com/jackc/pgx/v5/pgxpool"
)

func runtimeConfig(t *testing.T, x Engine, f httpMTLS) StagedGatewayRuntimeConfig {
	t.Helper()
	dir := "../../migrations"
	evidence, err := authorityschema.InspectIdentityEvidence(t.Context(), x.Pool, dir)
	if err != nil {
		t.Fatal(err)
	}
	return StagedGatewayRuntimeConfig{Enabled: true, Admission: guardedConfig(t, f, x), Management: quarantineServerConfig(f, x), MigrationDirectory: dir, SelectedAuthority: evidence}
}
func runtimeListener(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}
func startGatewayRuntime(t *testing.T, s *StagedGatewayRuntime) (string, string, context.CancelFunc, <-chan error) {
	t.Helper()
	a, m := runtimeListener(t), runtimeListener(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, a, m) }()
	t.Cleanup(cancel)
	return "https://" + a.Addr().String(), "https://" + m.Addr().String(), cancel, done
}
func TestGatewayRuntimeDefaultOffAndConstructorGuards(t *testing.T) {
	t.Parallel() // Each test owns a separate disposable DB and listeners.
	if s, err := NewStagedGatewayRuntime(t.Context(), StagedGatewayRuntimeConfig{}); s != nil || !errors.Is(err, ErrDisabled) {
		t.Fatal(s, err)
	}
	for _, mode := range []string{"nil-context", "admission-off", "management-off", "engine-off", "no-pool", "different-pool", "no-durable", "no-gateway", "no-witness"} {
		t.Run(mode, func(t *testing.T) {
			x := Engine{Enabled: true, Pool: &pgxpool.Pool{}}
			c := StagedGatewayRuntimeConfig{Enabled: true, Admission: AdmissionServerConfig{Enabled: true, Admission: AdmissionHTTPConfig{Engine: x, DurableDenials: true, GatewayAuthority: true}}, Management: GatewayQuarantineServerConfig{Enabled: true, Engine: x}}
			ctx := t.Context()
			switch mode {
			case "nil-context":
				ctx = nil
			case "admission-off":
				c.Admission.Enabled = false
			case "management-off":
				c.Management.Enabled = false
			case "engine-off":
				c.Management.Engine.Enabled = false
			case "no-pool":
				c.Admission.Admission.Engine.Pool = nil
			case "different-pool":
				c.Management.Engine.Pool = &pgxpool.Pool{}
			case "no-durable":
				c.Admission.Admission.DurableDenials = false
			case "no-gateway":
				c.Admission.Admission.GatewayAuthority = false
			}
			if s, err := NewStagedGatewayRuntime(ctx, c); s != nil || err == nil {
				t.Fatal(s, err)
			}
		})
	}
	x, _ := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	seedQuarantineSession(t, x)
	c := runtimeConfig(t, x, f)
	s, err := NewStagedGatewayRuntime(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	if s.admission.started || s.management.transport.started {
		t.Fatal("constructor started transport")
	}
	if err = s.WaitDrained(t.Context()); !errors.Is(err, ErrStagedGatewayRuntime) {
		t.Fatal("unstarted drain", err)
	}
	exec(t, x.Pool, "UPDATE users SET display_name='changed-since-original-witness' WHERE id='u'")
	if next, e := NewStagedGatewayRuntime(t.Context(), c); next != nil || e == nil {
		t.Fatal("stale witness accepted", next, e)
	}
	fresh := runtimeConfig(t, x, f)
	exec(t, x.Pool, "UPDATE gateways SET status='revoked' WHERE id='g'")
	// Refreshing a witness FROM this database is NOT provenance/permission, and
	// even matching selected equality cannot bypass actual route authority.
	fresh.SelectedAuthority, err = authorityschema.InspectIdentityEvidence(t.Context(), x.Pool, c.MigrationDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if next, e := NewStagedGatewayRuntime(t.Context(), fresh); next != nil || e == nil {
		t.Fatal("matching witness bypassed current binding", next, e)
	}
	t.Log("STAGED_RUNTIME_DEFAULT_OFF_SAME_DB_INDEPENDENT_WITNESS_AND_BINDING_PREFLIGHT_OK")
}
func TestGatewayRuntimeActualAdmissionManagementQuarantineAndDrain(t *testing.T) {
	t.Parallel() // Each test owns a separate disposable DB and listeners.
	x, e := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	seedQuarantineSession(t, x)
	c := runtimeConfig(t, x, f)
	s, err := NewStagedGatewayRuntime(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	a, m, cancel, done := startGatewayRuntime(t, s)
	defer cancel()
	if code, _ := durableRequest(t, f, a, e); code != 200 {
		t.Fatal(code)
	}
	// Management must not act as telemetry and vice versa. No client certificate
	// turns an unauthenticated admin call into a session decision.
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f.roots, Certificates: []tls.Certificate{f.client}}, DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 4 * time.Second}
	req, err := http.NewRequestWithContext(t.Context(), "POST", m+"/staged/processing-v1/quarantine-gateway", strings.NewReader(quarantineBody(t, f)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal(resp.StatusCode)
	}
	req, err = http.NewRequestWithContext(t.Context(), "POST", m+"/staged/processing-v1/quarantine-gateway", strings.NewReader(quarantineBody(t, f)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+quarantineToken(t, "a", "u", "session", "admin"))
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	quarantineState(t, x, "revoked", 1)
	if code, _ := durableRequest(t, f, a, e); code != 503 {
		t.Fatal("runtime replay bypassed quarantine", code)
	}
	counts(t, x.Pool, e.EventID, 1)
	cancel()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("runtime did not stop")
	}
	wait, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	if err = s.WaitDrained(wait); err != nil {
		t.Fatal(err)
	}
	if next, e := NewStagedGatewayRuntime(t.Context(), c); next != nil || e == nil {
		t.Fatal("old witness reconstructed quarantined runtime", next, e)
	}
	t.Log("STAGED_RUNTIME_ACTUAL_DUAL_TLS_ADMIT_ADMIN_QUARANTINE_REPLAY_REFUSAL_DRAIN_OK")
}

func TestGatewayRuntimeCancelledInvalidAndSingleUseListeners(t *testing.T) {
	t.Parallel() // Each test owns a separate disposable DB and listeners.
	x, _ := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	s, err := NewStagedGatewayRuntime(t.Context(), runtimeConfig(t, x, f))
	if err != nil {
		t.Fatal(err)
	}
	a, m := runtimeListener(t), runtimeListener(t)
	if err = s.Serve(nil, a, m); !errors.Is(err, ErrStagedGatewayRuntime) {
		t.Fatal(err)
	}
	var typedNil *net.TCPListener
	if err = s.Serve(t.Context(), typedNil, m); !errors.Is(err, ErrStagedGatewayRuntime) {
		t.Fatal(err)
	}
	if err = s.Serve(t.Context(), a, a); !errors.Is(err, ErrStagedGatewayRuntime) {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err = s.Serve(cancelled, a, m); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if s.started {
		t.Fatal("invalid call consumed listeners")
	}
	// Closed transport fails, cancels its sibling and permanently consumes BOTH
	// listeners, including when the sibling sees cancellation before starting.
	a.Close()
	if err = s.Serve(t.Context(), a, m); !errors.Is(err, ErrStagedGatewayRuntime) {
		t.Fatal(err)
	}
	if err = s.WaitDrained(t.Context()); err != nil {
		t.Fatal(err)
	}
	if conn, err := net.DialTimeout("tcp", m.Addr().String(), 100*time.Millisecond); err == nil {
		conn.Close()
		t.Fatal("sibling listener survived failure")
	}
	nextA, nextM := runtimeListener(t), runtimeListener(t)
	if err = s.Serve(t.Context(), nextA, nextM); !errors.Is(err, ErrStagedGatewayRuntime) {
		t.Fatal("reopened", err)
	}
	// Refused reused call did NOT take ownership of the new caller's sockets.
	conn, err := net.DialTimeout("tcp", nextA.Addr().String(), time.Second)
	if err != nil {
		t.Fatal("refused reuse closed caller listener", err)
	}
	conn.Close()
	t.Log("STAGED_RUNTIME_FAILED_LISTENER_CANCELS_SIBLING_ONCE_NO_REOPEN_OR_OWNERSHIP_LEAK_OK")
}

func TestGatewayRuntimeFailurePreservesActualHandlerDrain(t *testing.T) {
	t.Parallel() // Each test owns a separate disposable DB and listeners.
	x, _ := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	s, err := NewStagedGatewayRuntime(t.Context(), runtimeConfig(t, x, f))
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	// Deliberately noncooperative TEST handler. Actual DB cancellation is covered
	// separately; this tests compositional ownership, never a production override.
	s.management.transport.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); close(entered); <-release })
	a, m := runtimeListener(t), runtimeListener(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, a, m) }()
	client := quarantineServerClient(t, f)
	response := make(chan error, 1)
	go func() {
		r, e := client.Get("https://" + m.Addr().String() + "/")
		if r != nil {
			r.Body.Close()
		}
		response <- e
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not enter")
	}
	// A later listener failure must stop the OTHER transport too, without
	// reporting its force-closed but still-running handler as drained.
	a.Close()
	select {
	case err = <-done:
		if !errors.Is(err, ErrStagedGatewayDrain) {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("failed sibling did not stop runtime")
	}
	if err = <-response; err == nil {
		t.Fatal("uncompleted response falsely succeeded")
	}
	wait, stop := context.WithTimeout(t.Context(), 20*time.Millisecond)
	if err = s.WaitDrained(wait); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("unreturned handler reported drained", err)
	}
	stop()
	close(release)
	wait, stop = context.WithTimeout(t.Context(), time.Second)
	defer stop()
	if err = s.WaitDrained(wait); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("handler retried", calls.Load())
	}
	quarantineState(t, x, "active", 0)
	t.Log("STAGED_RUNTIME_FAILED_SIBLING_FORCE_CLOSE_NOT_HANDLER_DRAIN_NO_RETRY_OK")
}

func TestGatewayRuntimeActualBlockedManagementCancelDrainsBoth(t *testing.T) {
	t.Parallel() // Each test owns a separate disposable DB and listeners.
	x, _ := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	seedQuarantineSession(t, x)
	s, err := NewStagedGatewayRuntime(t.Context(), runtimeConfig(t, x, f))
	if err != nil {
		t.Fatal(err)
	}
	a, m, cancel, done := startGatewayRuntime(t, s)
	defer cancel()
	blocker, err := x.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(blocker)
	if _, err = blocker.Exec(t.Context(), "UPDATE users SET role='admin' WHERE id='u'"); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), "POST", m+"/staged/processing-v1/quarantine-gateway", strings.NewReader(quarantineBody(t, f)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+quarantineToken(t, "a", "u", "session", "admin"))
	request.Header.Set("Content-Type", "application/json")
	response := make(chan error, 1)
	client := quarantineServerClient(t, f)
	go func() {
		r, e := client.Do(request)
		if r != nil {
			r.Body.Close()
		}
		response <- e
	}()
	waitQuarantineSQL(t, x, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'SELECT role,disabled_at%'")
	cancel()
	select {
	case err = <-done:
		if err != nil && !errors.Is(err, ErrStagedGatewayDrain) {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("cancel did not stop transports")
	}
	if err = <-response; err == nil {
		t.Fatal("blocked decision falsely acknowledged")
	}
	wait, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	if err = s.WaitDrained(wait); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{a, m} {
		if conn, err := net.DialTimeout("tcp", strings.TrimPrefix(endpoint, "https://"), 100*time.Millisecond); err == nil {
			conn.Close()
			t.Fatal("listener survived cancellation", endpoint)
		}
	}
	rollback(blocker)
	quarantineState(t, x, "active", 0)
	t.Log("STAGED_RUNTIME_ACTUAL_BLOCKED_ADMIN_DB_CANCEL_BOTH_LISTENERS_ACTUAL_DRAIN_ZERO_WRITES_OK")
}
