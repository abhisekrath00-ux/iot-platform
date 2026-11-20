package ingestprocessing

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/authorityschema"
)

func seedIdentityEvidence(t *testing.T, x Engine, f httpMTLS) {
	t.Helper()
	bindGatewayLeaf(t, x, f)
	seedQuarantineSession(t, x)
	exec(t, x.Pool, `INSERT INTO customers(id,tenant_id,name) VALUES('cust','a','Synthetic');
 INSERT INTO edge_identity_migrations(id,tenant_id,gateway_id,serial,csr_pem,csr_sha256,key_sha256,state) VALUES('migration','a','g','SYNTHETIC','synthetic-public-csr','synthetic-csr-digest','synthetic-public-key-digest','issued');
 INSERT INTO edge_pairing_proofs(id,migration_id,tenant_id,gateway_id,cert_fingerprint,nonce_sha256) VALUES('proof','migration','a','g','synthetic-leaf-digest','synthetic-nonce-digest');
 INSERT INTO audit_log(tenant_id,actor,action,target,detail) VALUES('a','u','staged.gateway_quarantined','g','{"synthetic":true}');
 UPDATE gateways SET status='revoked' WHERE id='g'`)
}
func TestSelectedIdentityEvidenceExactStateAndMismatch(t *testing.T) {
	dir := filepath.Join("..", "..", "migrations")
	for _, mode := range []string{"status", "fingerprint", "gateway-tenant", "session-revocation", "session-removal", "user-role", "user-cutoff", "scope-insert", "audit-detail", "audit-removal", "migration-key", "pairing-proof", "tenant-name", "site-name", "customer-name", "denial"} {
		t.Run(mode, func(t *testing.T) {
			x, _ := denialFixture(t)
			f := httpTLSFixture(t)
			seedIdentityEvidence(t, x, f)
			before, err := authorityschema.InspectIdentityEvidence(t.Context(), x.Pool, dir)
			if err != nil || before.Tables != 12 || before.Rows < 10 || before.RuntimeAccepted || before.ProcessingEnabled {
				t.Fatal(before, err)
			}
			if err = authorityschema.VerifyIdentityEvidence(t.Context(), x.Pool, dir, before); err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(before)
			if err != nil || bytes.Contains(b, []byte("synthetic")) || bytes.Contains(b, []byte("session")) {
				t.Fatal("witness leaked selected values", string(b), err)
			}
			denialBefore, err := authorityschema.InspectEvidence(t.Context(), x.Pool, dir)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "status":
				exec(t, x.Pool, "UPDATE gateways SET status='active' WHERE id='g'")
			case "fingerprint":
				exec(t, x.Pool, "UPDATE gateways SET cert_fingerprint='changed' WHERE id='g'")
			case "gateway-tenant":
				exec(t, x.Pool, "UPDATE gateways SET tenant_id='b' WHERE id='g'")
			case "session-revocation":
				exec(t, x.Pool, "UPDATE user_sessions SET revoked_at=now() WHERE id='session'")
			case "session-removal":
				exec(t, x.Pool, "DELETE FROM user_sessions WHERE id='session'")
			case "user-role":
				exec(t, x.Pool, "UPDATE users SET role='viewer' WHERE id='u'")
			case "user-cutoff":
				exec(t, x.Pool, "UPDATE users SET tokens_valid_after=now() WHERE id='u'")
			case "scope-insert":
				exec(t, x.Pool, "INSERT INTO user_customer_scope(tenant_id,user_id,customer_id) VALUES('a','u','cust')")
			case "audit-detail":
				exec(t, x.Pool, "UPDATE audit_log SET detail='{\"changed\":true}'")
			case "audit-removal":
				exec(t, x.Pool, "DELETE FROM audit_log")
			case "migration-key":
				exec(t, x.Pool, "UPDATE edge_identity_migrations SET key_sha256='changed'")
			case "pairing-proof":
				exec(t, x.Pool, "UPDATE edge_pairing_proofs SET nonce_sha256='changed'")
			case "tenant-name":
				exec(t, x.Pool, "UPDATE tenants SET name='changed' WHERE id='a'")
			case "site-name":
				exec(t, x.Pool, "UPDATE sites SET name='changed' WHERE id='s'")
			case "customer-name":
				exec(t, x.Pool, "UPDATE customers SET name='changed' WHERE id='cust'")
			case "denial":
				if err = x.DenyLeaf(t.Context(), [32]byte{9}); err != nil {
					t.Fatal(err)
				}
			}
			if err = authorityschema.Inspect(t.Context(), x.Pool, dir); err != nil {
				t.Fatal("valid shape unexpectedly refused", err)
			}
			after, err := authorityschema.InspectIdentityEvidence(t.Context(), x.Pool, dir)
			if err != nil || after.SHA256 == before.SHA256 {
				t.Fatal("changed selected authority not detected", mode, after, err)
			}
			if err = authorityschema.VerifyIdentityEvidence(t.Context(), x.Pool, dir, before); err == nil {
				t.Fatal("old identity witness accepted", mode)
			}
			if mode != "denial" {
				if err = authorityschema.VerifyEvidence(t.Context(), x.Pool, dir, denialBefore); err != nil {
					t.Fatal("old denial-only semantics changed", err)
				}
			}
		})
	}
	t.Log("SELECTED_IDENTITY95_QUARANTINE_SESSION_SCOPE_AUDIT_BINDING_WITNESS_DETECTS_DRIFT_OK")
}
func TestSelectedIdentityEvidenceRefusesDriftBusyWritersAndInvalidWitness(t *testing.T) {
	dir := filepath.Join("..", "..", "migrations")
	x, _ := fixture(t)
	if _, err := authorityschema.InspectIdentityEvidence(t.Context(), x.Pool, dir); err == nil {
		t.Fatal("exact94 accepted")
	}
	x, _ = denialFixture(t)
	before, err := authorityschema.InspectIdentityEvidence(t.Context(), x.Pool, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*authorityschema.IdentityEvidence){func(e *authorityschema.IdentityEvidence) { e.Version = 2 }, func(e *authorityschema.IdentityEvidence) { e.Status = "other" }, func(e *authorityschema.IdentityEvidence) { e.Tables = 11 }, func(e *authorityschema.IdentityEvidence) { e.Rows = -1 }, func(e *authorityschema.IdentityEvidence) { e.Rows = 1200001 }, func(e *authorityschema.IdentityEvidence) { e.RuntimeAccepted = true }, func(e *authorityschema.IdentityEvidence) { e.ProcessingEnabled = true }, func(e *authorityschema.IdentityEvidence) { e.SHA256 = strings.ToUpper(e.SHA256) }} {
		bad := before
		change(&bad)
		if err = authorityschema.VerifyIdentityEvidence(t.Context(), x.Pool, dir, bad); err == nil {
			t.Fatal("invalid witness accepted")
		}
	}
	for _, statement := range []string{"UPDATE users SET display_name='pending' WHERE id='u'", "INSERT INTO user_customer_scope(tenant_id,user_id,customer_id) SELECT tenant_id,user_id,customer_id FROM user_customer_scope WHERE false", "UPDATE audit_log SET action=action"} {
		tx, err := x.Pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(t.Context(), statement); err != nil {
			rollback(tx)
			t.Fatal(err)
		}
		start := time.Now()
		if got, e := authorityschema.InspectIdentityEvidence(t.Context(), x.Pool, dir); e == nil || got.SHA256 != "" {
			rollback(tx)
			t.Fatal("busy writer accepted", got, e)
		}
		if time.Since(start) > time.Second {
			rollback(tx)
			t.Fatal("NOWAIT queued")
		}
		rollback(tx)
	}
	if err = authorityschema.VerifyIdentityEvidence(t.Context(), x.Pool, dir, before); err != nil {
		t.Fatal("busy refusal leaked lock or changed data", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if got, e := authorityschema.InspectIdentityEvidence(canceled, x.Pool, dir); e == nil || got.SHA256 != "" {
		t.Fatal(got, e)
	}
	var originalName string
	if err = x.Pool.QueryRow(t.Context(), "SELECT name FROM tenants WHERE id='a'").Scan(&originalName); err != nil {
		t.Fatal(err)
	}
	exec(t, x.Pool, "UPDATE tenants SET name=repeat('x',1048577) WHERE id='a'")
	if got, e := authorityschema.InspectIdentityEvidence(t.Context(), x.Pool, dir); e == nil || got.SHA256 != "" {
		t.Fatal("oversized selected row accepted", got, e)
	}
	exec(t, x.Pool, "UPDATE tenants SET name=$1 WHERE id='a'", originalName)
	if err = authorityschema.VerifyIdentityEvidence(t.Context(), x.Pool, dir, before); err != nil {
		t.Fatal("size refusal altered original authority", err)
	}
	exec(t, x.Pool, "ALTER TABLE user_sessions ADD COLUMN surprise text")
	if err = authorityschema.VerifyIdentityEvidence(t.Context(), x.Pool, dir, before); err == nil {
		t.Fatal("empty selected-table schema drift accepted")
	}
	exec(t, x.Pool, "ALTER TABLE admission_denied_leaf_v1 ADD COLUMN surprise text")
	if _, err = authorityschema.InspectIdentityEvidence(t.Context(), x.Pool, dir); err == nil {
		t.Fatal("damaged95 accepted")
	}
	t.Log("SELECTED_IDENTITY95_NOWAIT_ABSENT_SCOPE_WRITER_NO_REPAIR_NO_ACTIVATION_OK")
}
