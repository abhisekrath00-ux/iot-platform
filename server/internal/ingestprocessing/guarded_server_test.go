package ingestprocessing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func guardedConfig(t *testing.T, f httpMTLS, x Engine) AdmissionServerConfig {
	t.Helper()
	c := serverConfig(t, f, x)
	c.Admission.DurableDenials = true
	c.Admission.GatewayAuthority = true
	return c
}
func TestGuardedServerRequiresExplicitProtectionsBeforeDB(t *testing.T) {
	f := httpTLSFixture(t)
	for _, mode := range []string{"disabled", "durable", "gateway", "nil-context", "invalid-TLS"} {
		t.Run(mode, func(t *testing.T) {
			c := guardedConfig(t, f, Engine{Enabled: true, Pool: &pgxpool.Pool{}})
			ctx := t.Context()
			switch mode {
			case "disabled":
				c.Enabled = false
			case "durable":
				c.Admission.DurableDenials = false
			case "gateway":
				c.Admission.GatewayAuthority = false
			case "nil-context":
				ctx = nil
			case "invalid-TLS":
				c.ClientCAs = nil
			}
			if s, err := NewGuardedAdmissionServer(ctx, c, "unused"); s != nil || err == nil {
				t.Fatal("invalid assembly returned a server", err)
			}
		})
	}
}
func TestGuardedServerExact95AndEveryBindingBeforeReturn(t *testing.T) {
	dir := filepath.Join("..", "..", "migrations")
	t.Run("legacy94", func(t *testing.T) {
		x, _ := fixture(t)
		f := httpTLSFixture(t)
		bindGatewayLeaf(t, x, f)
		if s, err := NewGuardedAdmissionServer(t.Context(), guardedConfig(t, f, x), dir); s != nil || !errors.Is(err, ErrGuardedAdmissionServer) {
			t.Fatal(s, err)
		}
		var n int
		if err := x.Pool.QueryRow(t.Context(), "SELECT count(*) FROM schema_migrations").Scan(&n); err != nil || n != 94 {
			t.Fatal("preflight migrated", n, err)
		}
	})
	x, e := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	cfg := guardedConfig(t, f, x)
	for _, q := range []string{
		"UPDATE gateways SET status='revoked' WHERE id='g'",
		"UPDATE gateways SET cert_fingerprint=NULL WHERE id='g'",
		"UPDATE gateways SET tenant_id='b' WHERE id='g'",
	} {
		exec(t, x.Pool, q)
		if s, err := NewGuardedAdmissionServer(t.Context(), cfg, dir); s != nil || !errors.Is(err, ErrGuardedAdmissionServer) {
			t.Fatal(q, s, err)
		}
		exec(t, x.Pool, "UPDATE gateways SET status='active',tenant_id='a' WHERE id='g'")
		bindGatewayLeaf(t, x, f)
	}
	bad := cfg
	bad.Admission.Routes = append(append([]CertificateRoute(nil), cfg.Admission.Routes...), CertificateRoute{LeafSHA256: [32]byte{99}, Topic: "t/a/g/missing/telemetry"})
	if s, err := NewGuardedAdmissionServer(t.Context(), bad, dir); s != nil || !errors.Is(err, ErrGuardedAdmissionServer) {
		t.Fatal("one good route hid another invalid one", s, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if s, err := NewGuardedAdmissionServer(ctx, cfg, dir); s != nil || err == nil {
		t.Fatal("cancelled context accepted", s, err)
	}
	if s, err := NewGuardedAdmissionServer(t.Context(), cfg, t.TempDir()); s != nil || err == nil {
		t.Fatal("unverified catalog directory accepted", s, err)
	}
	counts(t, x.Pool, e.EventID, 0)
	exec(t, x.Pool, "ALTER TABLE admission_denied_leaf_v1 ADD COLUMN drift text")
	if s, err := NewGuardedAdmissionServer(t.Context(), cfg, dir); s != nil || !errors.Is(err, ErrGuardedAdmissionServer) {
		t.Fatal("catalog drift accepted", s, err)
	}
	t.Log("GUARDED_SERVER_EXACT95_EVERY_ROUTE_PREFLIGHT_REFUSAL_NO_WRITES_OK")
}
func TestGuardedServerPreflightIsNotLastingAuthorityRealTLS(t *testing.T) {
	x, e := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	cfg := guardedConfig(t, f, x)
	s, err := NewGuardedAdmissionServer(t.Context(), cfg, filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	// Mutating the caller's flags/routes cannot downgrade the assembled handler.
	cfg.Admission.DurableDenials = false
	cfg.Admission.GatewayAuthority = false
	cfg.Admission.Routes[0].Topic = "t/b/g/g/telemetry"
	endpoint := startAdmissionServer(t, s)
	if code, _ := durableRequest(t, f, endpoint, e); code != 200 {
		t.Fatal(code)
	}
	counts(t, x.Pool, e.EventID, 1)
	exec(t, x.Pool, "UPDATE gateways SET status='revoked' WHERE id='g'")
	if code, _ := durableRequest(t, f, endpoint, e); code != 503 {
		t.Fatal("startup check replaced request guard", code)
	}
	exec(t, x.Pool, "UPDATE gateways SET status='active' WHERE id='g'")
	if err = x.DenyLeaf(t.Context(), sha256.Sum256(f.client.Certificate[0])); err != nil {
		t.Fatal(err)
	}
	if code, _ := durableRequest(t, f, endpoint, e); code != 503 {
		t.Fatal("startup check replaced durable denial", code)
	}
	cfg = guardedConfig(t, f, x)
	if next, err := NewGuardedAdmissionServer(t.Context(), cfg, filepath.Join("..", "..", "migrations")); next != nil || !errors.Is(err, ErrGuardedAdmissionServer) {
		t.Fatal("denied configured leaf accepted at reconstruction", next, err)
	}
	counts(t, x.Pool, e.EventID, 1)
	for _, table := range []string{"flow_runs", "alerts", "commands"} {
		var n int
		if err = x.Pool.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
	t.Log("GUARDED_SERVER_REAL_PG_MTLS_PREFLIGHT_NOT_AUTHORITY_FROZEN_GUARDS_OK")
}

func TestGuardedServerMultipleValidRoutesAndNoAuthorityWrites(t *testing.T) {
	x, e := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	other := httpTLSFixture(t)
	fp := sha256.Sum256(other.client.Certificate[0])
	exec(t, x.Pool, "INSERT INTO sites(id,tenant_id,name) VALUES('sb','b','Synthetic B');INSERT INTO gateways(id,tenant_id,site_id,serial,status) VALUES('gb','b','sb','SYNTHETIC_B','active')")
	exec(t, x.Pool, "UPDATE gateways SET cert_fingerprint=$1 WHERE id='gb'", hex.EncodeToString(fp[:]))
	cfg := guardedConfig(t, f, x)
	cfg.Admission.Routes = append(cfg.Admission.Routes, CertificateRoute{LeafSHA256: fp, Topic: "t/b/g/gb/telemetry"})
	s, err := NewGuardedAdmissionServer(t.Context(), cfg, filepath.Join("..", "..", "migrations"))
	if err != nil || s == nil {
		t.Fatal(err)
	}
	counts(t, x.Pool, e.EventID, 0)
	var revision, denied int
	if err = x.Pool.QueryRow(t.Context(), "SELECT revision,(SELECT count(*) FROM admission_denied_leaf_v1) FROM admission_denial_epoch_v1 WHERE singleton=true").Scan(&revision, &denied); err != nil || revision != 0 || denied != 0 {
		t.Fatal(revision, denied, err)
	}
	t.Log("GUARDED_SERVER_MULTIPLE_CURRENT_ROUTES_ZERO_AUTHORITY_WRITES_OK")
}
func TestGuardedServerOldSnapshotAtConstructionRefuses(t *testing.T) {
	x, _ := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	cfg := guardedConfig(t, f, x)
	block, err := x.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(block)
	if _, err = block.Exec(t.Context(), "SELECT pg_advisory_xact_lock(731492068214)"); err != nil {
		t.Fatal(err)
	}
	type result struct {
		s   *AdmissionServer
		err error
	}
	done := make(chan result, 1)
	go func() {
		s, err := NewGuardedAdmissionServer(t.Context(), cfg, filepath.Join("..", "..", "migrations"))
		done <- result{s, err}
	}()
	deadline := time.Now().Add(3 * time.Second)
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
			t.Fatal("no construction waiter")
		}
		time.Sleep(time.Millisecond)
	}
	exec(t, x.Pool, "UPDATE gateways SET status='revoked' WHERE id='g'")
	if err = block.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.s != nil || !errors.Is(got.err, ErrGuardedAdmissionServer) {
		t.Fatal("old snapshot returned server", got.err)
	}
	t.Log("GUARDED_SERVER_OLD_CONSTRUCTION_SNAPSHOT_REFUSED_OK")
}
