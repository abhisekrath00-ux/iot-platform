package ingestprocessing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func bindGatewayLeaf(t *testing.T, x Engine, f httpMTLS) {
	t.Helper()
	leaf := sha256.Sum256(f.client.Certificate[0])
	exec(t, x.Pool, "UPDATE gateways SET cert_fingerprint=$1 WHERE id='g' AND tenant_id='a'", hex.EncodeToString(leaf[:]))
}
func TestGatewayAuthorityRequiresDurableDenials(t *testing.T) {
	f := httpTLSFixture(t)
	c := httpConfig(t, f, Engine{Enabled: true, Pool: &pgxpool.Pool{}})
	c.GatewayAuthority = true
	if h, err := NewAdmissionHTTP(c); h != nil || err == nil {
		t.Fatal("unguarded enrollment option accepted")
	}
	c.DurableDenials = true
	if h, err := NewAdmissionHTTP(c); h == nil || err != nil {
		t.Fatal(err)
	}
	x, e := fixture(t)
	x.gatewayAuthority = true
	if _, err := x.Admit(t.Context(), e); err == nil {
		t.Fatal("no verified leaf accepted")
	}
	counts(t, x.Pool, e.EventID, 0)
}
func TestGatewayAuthorityRealTLSCurrentBindingAndExactReplay(t *testing.T) {
	x, e := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	cfg := serverConfig(t, f, x)
	cfg.Admission.DurableDenials = true
	cfg.Admission.GatewayAuthority = true
	s, err := NewAdmissionServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := startAdmissionServer(t, s)
	if code, _ := durableRequest(t, f, endpoint, e); code != 200 {
		t.Fatal(code)
	}
	counts(t, x.Pool, e.EventID, 1)
	for _, q := range []string{
		"UPDATE gateways SET status='revoked' WHERE id='g'",
		"UPDATE gateways SET cert_fingerprint=NULL WHERE id='g'",
		"UPDATE gateways SET cert_fingerprint=upper(cert_fingerprint) WHERE id='g'",
		"UPDATE gateways SET cert_fingerprint=repeat('0',64) WHERE id='g'",
		"UPDATE gateways SET kind='direct' WHERE id='g'",
		"UPDATE gateways SET auth_mode='password' WHERE id='g'",
		"UPDATE gateways SET tenant_id='b' WHERE id='g'",
	} {
		t.Run(q, func(t *testing.T) {
			exec(t, x.Pool, q)
			if code, b := durableRequest(t, f, endpoint, e); code != 503 || strings.Contains(string(b), "signature") {
				t.Fatal(code, string(b))
			}
			counts(t, x.Pool, e.EventID, 1)
			fresh, err := NewAdmissionServer(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if code, b := durableRequest(t, f, startAdmissionServer(t, fresh), e); code != 503 || strings.Contains(string(b), "signature") {
				t.Fatal(code, string(b))
			}
			exec(t, x.Pool, "UPDATE gateways SET status='active',kind='edge',auth_mode='cert',tenant_id='a' WHERE id='g'")
			bindGatewayLeaf(t, x, f)
		})
	}
	// Legacy opt-out still bypasses CURRENT gateway state even with durable
	// denials enabled. This is an explicit activation boundary, not silently fixed.
	exec(t, x.Pool, "UPDATE gateways SET status='revoked' WHERE id='g'")
	legacy := cfg
	legacy.Admission.GatewayAuthority = false
	old, err := NewAdmissionServer(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := durableRequest(t, f, startAdmissionServer(t, old), e); code != 200 {
		t.Fatal("legacy static-route limitation changed", code)
	}
	exec(t, x.Pool, "UPDATE gateways SET status='active' WHERE id='g'")
	// NO mutation/denial was performed by failed requests. Restoring the current
	// enrollment by this test's direct maintenance allows the same exact replay.
	// Operational recovery/rebind authorization remains outside this primitive.
	if code, _ := durableRequest(t, f, endpoint, e); code != 200 {
		t.Fatal(code)
	}
	counts(t, x.Pool, e.EventID, 1)
	leaf := sha256.Sum256(f.client.Certificate[0])
	if err = x.DenyLeaf(t.Context(), leaf); err != nil {
		t.Fatal(err)
	}
	if code, b := durableRequest(t, f, endpoint, e); code != 503 || strings.Contains(string(b), "signature") {
		t.Fatal(code, string(b))
	}
	for _, table := range []string{"flow_runs", "alerts", "commands"} {
		var n int
		if err = x.Pool.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
	t.Log("LIVE_CURRENT_GATEWAY_AUTHORITY_PG_MTLS_EXACT_REPLAY_AND_RECONSTRUCTION_REFUSAL_OK")
}
func TestGatewayAuthorityRealTLSOldSerializableSnapshot(t *testing.T) {
	x, e := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	cfg := serverConfig(t, f, x)
	cfg.Admission.DurableDenials = true
	cfg.Admission.GatewayAuthority = true
	s, err := NewAdmissionServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := startAdmissionServer(t, s)
	block, err := x.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(block)
	if _, err = block.Exec(t.Context(), "SELECT pg_advisory_xact_lock(731492068214)"); err != nil {
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
			t.Fatal("no admission lock waiter")
		}
		time.Sleep(time.Millisecond)
	}
	exec(t, x.Pool, "UPDATE gateways SET status='revoked' WHERE id='g'")
	if err = block.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if o := <-done; o.err != nil || o.code != 503 || strings.Contains(string(o.body), "signature") {
		t.Fatal("old snapshot accepted", o.code, o.err)
	}
	counts(t, x.Pool, e.EventID, 0)
	t.Log("LIVE_CURRENT_GATEWAY_AUTHORITY_OLD_SNAPSHOT_REFUSED_OK")
}
func TestGatewayAuthorityEnteredRealTLSCommitBeforeIdentityChange(t *testing.T) {
	x, e := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	cfg := serverConfig(t, f, x)
	cfg.Admission.DurableDenials = true
	cfg.Admission.GatewayAuthority = true
	s, err := NewAdmissionServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := startAdmissionServer(t, s)
	exec(t, x.Pool, `CREATE FUNCTION hold_gateway_insert() RETURNS trigger AS $$ BEGIN PERFORM pg_advisory_xact_lock(731492068216);RETURN NEW;END $$ LANGUAGE plpgsql;CREATE TRIGGER hold_gateway_insert BEFORE INSERT ON telemetry FOR EACH ROW EXECUTE FUNCTION hold_gateway_insert()`)
	block, err := x.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(block)
	if _, err = block.Exec(t.Context(), "SELECT pg_advisory_xact_lock(731492068216)"); err != nil {
		t.Fatal(err)
	}
	done := make(chan durableOutcome, 1)
	payload := wirePayload(t, e)
	go func() { done <- requestDurable(t.Context(), f, endpoint, payload) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var n int
		err = x.Pool.QueryRow(t.Context(), "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory' AND query LIKE 'INSERT INTO telemetry%'").Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no actual insert waiter")
		}
		time.Sleep(time.Millisecond)
	}
	changed := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		_, err := x.Pool.Exec(ctx, "UPDATE gateways SET status='revoked' WHERE id='g'")
		changed <- err
	}()
	select {
	case err := <-changed:
		t.Fatal("identity changed before entered transaction ended", err)
	case <-time.After(30 * time.Millisecond):
	}
	if err = block.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if o := <-done; o.err != nil || o.code != 200 {
		t.Fatal(o.code, o.err)
	}
	if err = <-changed; err != nil {
		t.Fatal(err)
	}
	counts(t, x.Pool, e.EventID, 1)
	if code, b := durableRequest(t, f, endpoint, e); code != 503 || strings.Contains(string(b), "signature") {
		t.Fatal(code, string(b))
	}
	counts(t, x.Pool, e.EventID, 1)
	t.Log("LIVE_CURRENT_GATEWAY_AUTHORITY_SERIALIZES_AFTER_ENTERED_TLS_COMMIT_OK")
}
