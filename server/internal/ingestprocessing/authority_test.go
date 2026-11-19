package ingestprocessing

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/authorityschema"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/processingschema"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/setupbootstrap"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func denialFixture(t *testing.T) (Engine, Envelope) {
	t.Helper()
	x, e := fixture(t)
	x.Pool.Reset()
	if err := authorityschema.Apply(t.Context(), x.Pool, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatal(err)
	}
	if err := authorityschema.Inspect(t.Context(), x.Pool, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatal(err)
	}
	return x, e
}
func TestDurableDenial95StrictSchema(t *testing.T) {
	x, _ := denialFixture(t)
	dir := filepath.Join("..", "..", "migrations")
	if _, e := processingschema.Inspect(t.Context(), x.Pool, dir); e == nil {
		t.Fatal("old exact94 inspector accepted95")
	}
	if _, e := setupbootstrap.InspectReviewed(t.Context(), x.Pool, dir); e == nil {
		t.Fatal("old exact93 inspector accepted95")
	}
	if e := authorityschema.Apply(t.Context(), x.Pool, dir); e == nil {
		t.Fatal("blind reapply95 accepted")
	}
	var n int
	if e := x.Pool.QueryRow(t.Context(), "SELECT count(*) FROM schema_migrations").Scan(&n); e != nil || n != 95 {
		t.Fatal(n, e)
	}
	exec(t, x.Pool, "ALTER TABLE admission_denied_leaf_v1 ADD COLUMN surprise text")
	if e := authorityschema.Inspect(t.Context(), x.Pool, dir); e == nil {
		t.Fatal("drift accepted")
	}
}
func TestDurableDenialMissingAndDamagedAuthorityRefuses(t *testing.T) {
	x, e := fixture(t)
	leaf := [32]byte{1}
	guarded := x
	guarded.durableLeaf = &leaf
	if _, err := guarded.Admit(t.Context(), e); !errors.Is(err, ErrDurableDenial) {
		t.Fatal(err)
	}
	counts(t, x.Pool, e.EventID, 0)
	if err := x.DenyLeaf(t.Context(), leaf); !errors.Is(err, ErrDurableDenial) {
		t.Fatal(err)
	}
	x.Pool.Reset()
	if err := authorityschema.Apply(t.Context(), x.Pool, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatal(err)
	}
	exec(t, x.Pool, "DELETE FROM admission_denial_epoch_v1")
	if _, err := guarded.Admit(t.Context(), e); !errors.Is(err, ErrDurableDenial) {
		t.Fatal(err)
	}
	counts(t, x.Pool, e.EventID, 0)
	if err := x.DenyLeaf(t.Context(), leaf); !errors.Is(err, ErrDurableDenial) {
		t.Fatal(err)
	}
	if err := authorityschema.Inspect(t.Context(), x.Pool, filepath.Join("..", "..", "migrations")); err == nil {
		t.Fatal("absent epoch accepted")
	}
}
func TestDurableDenialRefusesInvalidCaller(t *testing.T) {
	if e := (Engine{}).DenyLeaf(t.Context(), [32]byte{1}); !errors.Is(e, ErrDisabled) {
		t.Fatal(e)
	}
	for _, ctx := range []context.Context{nil, t.Context()} {
		if e := (Engine{Enabled: true}).DenyLeaf(ctx, [32]byte{1}); !errors.Is(e, ErrDurableDenial) {
			t.Fatal(e)
		}
	}
	x, _ := denialFixture(t)
	if e := x.DenyLeaf(t.Context(), [32]byte{}); !errors.Is(e, ErrDurableDenial) {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if e := x.DenyLeaf(ctx, [32]byte{1}); e == nil {
		t.Fatal("cancelled denial accepted")
	}
}
func TestDurableDenialWaitsForEnteredTransaction(t *testing.T) {
	x, e := denialFixture(t)
	leaf := [32]byte{2}
	tx, err := x.Pool.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	if err = guardDurableLeaf(t.Context(), tx, leaf); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- x.DenyLeaf(t.Context(), leaf) }()
	select {
	case err := <-finished:
		t.Fatal("denial committed before admitted transaction drained", err)
	case <-time.After(30 * time.Millisecond):
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
	// Exact input stays blocked after confirmation. Earlier admission would be
	// allowed to commit BEFORE denial, never retroactively rolled back.
	x.durableLeaf = &leaf
	if _, err = x.Admit(t.Context(), e); !errors.Is(err, ErrDurableDenial) {
		t.Fatal(err)
	}
	counts(t, x.Pool, e.EventID, 0)
}

type durableOutcome struct {
	code int
	body []byte
	err  error
}

func requestDurable(ctx context.Context, f httpMTLS, endpoint string, payload []byte) durableOutcome {
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f.roots, Certificates: []tls.Certificate{f.client}}, DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	c := &http.Client{Transport: tr, Timeout: 4 * time.Second}
	r, err := http.NewRequestWithContext(ctx, "POST", endpoint+"/staged/processing-v1/admit", strings.NewReader(string(payload)))
	if err != nil {
		return durableOutcome{err: err}
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-HexThings-Telemetry-Topic", "t/a/g/g/telemetry")
	resp, err := c.Do(r)
	if err != nil {
		return durableOutcome{err: err}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return durableOutcome{resp.StatusCode, b, err}
}
func durableRequest(t *testing.T, f httpMTLS, endpoint string, e Envelope) (int, []byte) {
	t.Helper()
	o := requestDurable(t.Context(), f, endpoint, wirePayload(t, e))
	if o.err != nil {
		t.Fatal(o.err)
	}
	return o.code, o.body
}

func TestDurableDenialRealTLSReconstructionAndReplayRefusal(t *testing.T) {
	x, e := denialFixture(t)
	f := httpTLSFixture(t)
	cfg := serverConfig(t, f, x)
	cfg.Admission.DurableDenials = true
	s, err := NewAdmissionServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := startAdmissionServer(t, s)
	if code, b := durableRequest(t, f, endpoint, e); code != 200 || !strings.Contains(string(b), "signature") {
		t.Fatal(code, string(b))
	}
	counts(t, x.Pool, e.EventID, 1)
	leaf := sha256.Sum256(f.client.Certificate[0])
	if err = x.DenyLeaf(t.Context(), leaf); err != nil {
		t.Fatal(err)
	}
	if err = x.DenyLeaf(t.Context(), leaf); err != nil {
		t.Fatal(err)
	}
	// Existing live handler refuses without a process-local RevokeRoute call.
	if code, b := durableRequest(t, f, endpoint, e); code != 503 || strings.Contains(string(b), "signature") {
		t.Fatal(code, string(b))
	}
	// Close the original pool too. A NEW pool/server with identical stale route
	// configuration reads the persisted denial, not constructor/process-local state.
	dbcfg := x.Pool.Config()
	x.Pool.Close()
	freshPool, err := pgxpool.NewWithConfig(t.Context(), dbcfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(freshPool.Close)
	x.Pool = freshPool
	cfg.Admission.Engine.Pool = freshPool
	s, err = NewAdmissionServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	endpoint = startAdmissionServer(t, s)
	if code, b := durableRequest(t, f, endpoint, e); code != 503 || strings.Contains(string(b), "signature") {
		t.Fatal(code, string(b))
	}
	// Explicit legacy opt-out STILL BYPASSES this protection. Preserve this as a
	// tested activation gate, never claim that old/non-durable constructors are safe.
	legacy := cfg
	legacy.Admission.DurableDenials = false
	old, err := NewAdmissionServer(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := durableRequest(t, f, startAdmissionServer(t, old), e); code != 200 {
		t.Fatal("legacy limitation changed", code)
	}
	// Distinct currently unrevoked verified leaf remains separate, not tenantwide.
	cfg.Admission.Routes[0].LeafSHA256 = sha256.Sum256(f.foreign.Certificate[0])
	s, err = NewAdmissionServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	endpoint = startAdmissionServer(t, s)
	f.client = f.foreign
	e.EventID = "other-leaf"
	if code, _ := durableRequest(t, f, endpoint, e); code != 200 {
		t.Fatal(code)
	}
	counts(t, x.Pool, "input", 1)
	counts(t, x.Pool, e.EventID, 1)
	for _, table := range []string{"flow_runs", "alerts", "commands"} {
		var n int
		if err = x.Pool.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
	t.Log("LIVE_DURABLE_DENIAL_PG_MTLS_STALE_RECONSTRUCTION_REPLAY_REFUSAL_OK")
}
func TestDurableDenialRealTLSStaleSerializableSnapshot(t *testing.T) {
	x, e := denialFixture(t)
	f := httpTLSFixture(t)
	cfg := serverConfig(t, f, x)
	cfg.Admission.DurableDenials = true
	s, err := NewAdmissionServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := startAdmissionServer(t, s)
	blocker, err := x.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(blocker)
	if _, err = blocker.Exec(t.Context(), "SELECT pg_advisory_xact_lock(731492068214)"); err != nil {
		t.Fatal(err)
	}
	done := make(chan durableOutcome, 1)
	payload := wirePayload(t, e)
	go func() { done <- requestDurable(t.Context(), f, endpoint, payload) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var n int
		err = x.Pool.QueryRow(t.Context(), "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory' AND pid<>pg_backend_pid()").Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no real admission lock waiter")
		}
		time.Sleep(time.Millisecond)
	}
	// Request already took an OLD serializable snapshot before lock wait. Denial
	// commits now. FOR SHARE on changed epoch MUST serialize-fail, never miss it.
	if err = x.DenyLeaf(t.Context(), sha256.Sum256(f.client.Certificate[0])); err != nil {
		t.Fatal(err)
	}
	if err = blocker.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if o := <-done; o.err != nil || o.code != 503 || strings.Contains(string(o.body), "signature") {
		t.Fatal("stale snapshot accepted", o.code, o.err, string(o.body))
	}
	counts(t, x.Pool, e.EventID, 0)
	if code, _ := durableRequest(t, f, endpoint, e); code != 503 {
		t.Fatal(code)
	}
	counts(t, x.Pool, e.EventID, 0)
	t.Log("LIVE_DURABLE_DENIAL_OLD_SERIALIZABLE_SNAPSHOT_REFUSED_OK")
}

func TestDurableDenial95UnrecordedObjectAndMaintenanceRefusal(t *testing.T) {
	x, _ := fixture(t)
	dir := filepath.Join("..", "..", "migrations")
	exec(t, x.Pool, "CREATE TABLE admission_denied_leaf_v1(surprise text)")
	x.Pool.Reset()
	if err := authorityschema.Apply(t.Context(), x.Pool, dir); err == nil {
		t.Fatal("unrecorded object repaired")
	}
	var n int
	if err := x.Pool.QueryRow(t.Context(), "SELECT count(*) FROM schema_migrations").Scan(&n); err != nil || n != 94 {
		t.Fatal(n, err)
	}
	exec(t, x.Pool, "DROP TABLE admission_denied_leaf_v1")
	held, err := x.Pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = authorityschema.Apply(t.Context(), x.Pool, dir); err == nil {
		t.Fatal("other active DB client accepted")
	}
	held.Release()
	if err = x.Pool.QueryRow(t.Context(), "SELECT count(*) FROM schema_migrations").Scan(&n); err != nil || n != 94 {
		t.Fatal(n, err)
	}
}

func TestDurableDenialActuallyEnteredTLSCommitBeforeDenial(t *testing.T) {
	x, e := denialFixture(t)
	f := httpTLSFixture(t)
	cfg := serverConfig(t, f, x)
	cfg.Admission.DurableDenials = true
	s, err := NewAdmissionServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := startAdmissionServer(t, s)
	exec(t, x.Pool, `CREATE FUNCTION hold_durable_insert() RETURNS trigger AS $$ BEGIN PERFORM pg_advisory_xact_lock(731492068215);RETURN NEW;END $$ LANGUAGE plpgsql;CREATE TRIGGER hold_durable_insert BEFORE INSERT ON telemetry FOR EACH ROW EXECUTE FUNCTION hold_durable_insert()`)
	blocker, err := x.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(blocker)
	if _, err = blocker.Exec(t.Context(), "SELECT pg_advisory_xact_lock(731492068215)"); err != nil {
		t.Fatal(err)
	}
	done := make(chan durableOutcome, 1)
	payload := wirePayload(t, e)
	go func() { done <- requestDurable(t.Context(), f, endpoint, payload) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var n int
		err = x.Pool.QueryRow(t.Context(), "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory' AND query LIKE 'INSERT INTO telemetry%' ").Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no actual insert waiter after guard")
		}
		time.Sleep(time.Millisecond)
	}
	denied := make(chan error, 1)
	go func() { denied <- x.DenyLeaf(t.Context(), sha256.Sum256(f.client.Certificate[0])) }()
	select {
	case err := <-denied:
		t.Fatal("denial committed before guarded insert", err)
	case <-time.After(30 * time.Millisecond):
	}
	if err = blocker.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if o := <-done; o.err != nil || o.code != 200 {
		t.Fatal(o.code, o.err)
	}
	if err = <-denied; err != nil {
		t.Fatal(err)
	}
	counts(t, x.Pool, e.EventID, 1)
	if code, b := durableRequest(t, f, endpoint, e); code != 503 || strings.Contains(string(b), "signature") {
		t.Fatal(code, string(b))
	}
	counts(t, x.Pool, e.EventID, 1)
	t.Log("LIVE_DURABLE_DENIAL_SERIALIZES_AFTER_ENTERED_REAL_TLS_COMMIT_OK")
}
