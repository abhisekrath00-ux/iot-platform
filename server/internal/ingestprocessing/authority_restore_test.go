package ingestprocessing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/authorityschema"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDenialEvidenceContinuityAndMismatch(t *testing.T) {
	x, _ := denialFixture(t)
	dir := filepath.Join("..", "..", "migrations")
	before, err := authorityschema.InspectEvidence(t.Context(), x.Pool, dir)
	if err != nil || before.Revision != 0 || before.DeniedLeaves != 0 || before.RuntimeAccepted || before.ProcessingEnabled {
		t.Fatal(before, err)
	}
	if err = authorityschema.VerifyEvidence(t.Context(), x.Pool, dir, before); err != nil {
		t.Fatal(err)
	}
	leaf := [32]byte{3}
	if err = x.DenyLeaf(t.Context(), leaf); err != nil {
		t.Fatal(err)
	}
	after, err := authorityschema.InspectEvidence(t.Context(), x.Pool, dir)
	if err != nil || after.Revision != 1 || after.DeniedLeaves != 1 || after.SHA256 == before.SHA256 {
		t.Fatal(after, err)
	}
	if err = authorityschema.VerifyEvidence(t.Context(), x.Pool, dir, before); err == nil {
		t.Fatal("old baseline accepted")
	}
	if err = authorityschema.VerifyEvidence(t.Context(), x.Pool, dir, after); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*authorityschema.Evidence){func(e *authorityschema.Evidence) { e.Version = 2 }, func(e *authorityschema.Evidence) { e.Revision = -1 }, func(e *authorityschema.Evidence) { e.DeniedLeaves = -1 }, func(e *authorityschema.Evidence) { e.SHA256 = strings.ToUpper(e.SHA256) }, func(e *authorityschema.Evidence) { e.Status = "other" }, func(e *authorityschema.Evidence) { e.RuntimeAccepted = true }, func(e *authorityschema.Evidence) { e.ProcessingEnabled = true }} {
		bad := after
		change(&bad)
		if err = authorityschema.VerifyEvidence(t.Context(), x.Pool, dir, bad); err == nil {
			t.Fatal("invalid evidence accepted")
		}
	}
	// Same count/revision but changed content MUST NOT pass continuity.
	exec(t, x.Pool, "UPDATE admission_denied_leaf_v1 SET leaf_sha256=$1", bytes.Repeat([]byte{4}, 32))
	if err = authorityschema.VerifyEvidence(t.Context(), x.Pool, dir, after); err == nil {
		t.Fatal("same-size altered authority accepted")
	}
	exec(t, x.Pool, "UPDATE admission_denied_leaf_v1 SET leaf_sha256=$1", leaf[:])
	exec(t, x.Pool, "UPDATE admission_denied_leaf_v1 SET denied_at=denied_at+interval '1 microsecond'")
	if err = authorityschema.VerifyEvidence(t.Context(), x.Pool, dir, after); err == nil {
		t.Fatal("changed exact row timestamp accepted")
	}
}

// Optional actual-binary operator + pg_dump/pg_restore proof. No owner service,
// immutable image, old image switch or general backup-restore acceptance.
func TestDenialActualOperatorLogicalBackupRestore(t *testing.T) {
	operator, dump, restore := os.Getenv("TEST_DENIAL_SETUPCTL"), os.Getenv("TEST_DENIAL_PG_DUMP"), os.Getenv("TEST_DENIAL_PG_RESTORE")
	if operator == "" || dump == "" || restore == "" {
		t.Skip("run scripts/test-denial-authority-restore.sh for actual binary logical restore")
	}
	x, e := fixture(t)
	dir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := x.Pool.Config()
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + cfg.ConnConfig.Database
	dsn := u.String()
	command := func(program string, args []string, stdin []byte, env []string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		defer cancel()
		cmd := osexec.CommandContext(ctx, program, args...)
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}, env...)
		cmd.Stdin = bytes.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated command failed %s: %v %s", filepath.Base(program), err, out)
		}
		return out
	}
	op := func(action, db string, input []byte) []byte {
		return command(operator, []string{action}, input, []string{"DATABASE_URL=" + db, "MIGRATIONS_DIR=" + dir})
	}
	x.Pool.Reset()
	b := op("apply-staged-denials", dsn, []byte(`{"version":1,"scope":"staged_denial_v1","writers_stopped":true,"backup_verified":true}`))
	var installed authorityschema.Evidence
	if json.Unmarshal(b, &installed) != nil || installed.RuntimeAccepted || installed.ProcessingEnabled || installed.Revision != 0 {
		t.Fatal("operator claimed activation", string(b))
	}
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	config := serverConfig(t, f, x)
	config.Admission.DurableDenials = true
	config.Admission.GatewayAuthority = true
	s, err := NewGuardedAdmissionServer(t.Context(), config, dir)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := startAdmissionServer(t, s)
	if code, _ := durableRequest(t, f, endpoint, e); code != 200 {
		t.Fatal(code)
	}
	// Exercise the separately constructed ACTUAL management transport before backup.
	seedQuarantineSession(t, x)
	management := mustQuarantineServer(t, f, x)
	adminURL := startAdmissionServer(t, management.transport)
	client := quarantineServerClient(t, f)
	request, err := http.NewRequestWithContext(t.Context(), "POST", adminURL+"/staged/processing-v1/quarantine-gateway", strings.NewReader(quarantineBody(t, f)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+quarantineToken(t, "a", "u", "session", "admin"))
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	quarantineState(t, x, "revoked", 1)
	// Preserve an explicitly revoked OTHER session and a scope row for another
	// user. The admin that made the decision remains unscoped and tracked.
	exec(t, x.Pool, `INSERT INTO user_sessions(id,tenant_id,user_id,revoked_at) VALUES('revoked-session','a','u',now());
 INSERT INTO customers(id,tenant_id,name) VALUES('restore-customer','a','Synthetic');
 INSERT INTO user_customer_scope(tenant_id,user_id,customer_id) VALUES('a','other-user','restore-customer')`)
	leaf := sha256.Sum256(f.client.Certificate[0])
	if err = x.DenyLeaf(t.Context(), leaf); err != nil {
		t.Fatal(err)
	}
	original, err := x.Read(t.Context(), "a", e.EventID, e.ObservedAt)
	if err != nil {
		t.Fatal(err)
	}
	originalBytes, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	exec(t, x.Pool, `UPDATE devices SET config='{"port":"PRESERVE-SYNTHETIC","slave_id":11}' WHERE id='d'`)
	// All test writers are now stopped. Capture independent ORIGINAL authority
	// witness before creating the backup. Do not derive this baseline from restore.
	x.Pool.Reset()
	witness := op("inspect-staged-denials", dsn, nil)
	var expected authorityschema.Evidence
	if json.Unmarshal(witness, &expected) != nil || expected.DeniedLeaves != 1 || expected.Revision != 1 {
		t.Fatal(string(witness))
	}
	identityWitness := op("inspect-staged-identity", dsn, nil)
	var identityExpected authorityschema.IdentityEvidence
	if json.Unmarshal(identityWitness, &identityExpected) != nil || identityExpected.Tables != 12 || identityExpected.RuntimeAccepted || identityExpected.ProcessingEnabled {
		t.Fatal("identity operator claimed activation", string(identityWitness))
	}
	backup := filepath.Join(t.TempDir(), "isolated-denial95.dump")
	command(dump, []string{"--format=custom", "--no-owner", "--no-privileges", "--file", backup, dsn}, nil, nil)
	// Scratch restore destination is a NEW EMPTY database, never source overwrite.
	name := fmt.Sprintf("denial_restore_%d", time.Now().UnixNano())
	admin, err := pgxpool.New(t.Context(), os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		defer admin.Close()
		if _, err := admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Error(err)
		}
	})
	u.Path = "/" + name
	restoredDSN := u.String()
	command(restore, []string{"--exit-on-error", "--no-owner", "--no-privileges", "--dbname", restoredDSN, backup}, nil, nil)
	if b = op("verify-staged-denials", restoredDSN, witness); !bytes.Equal(bytes.TrimSpace(b), bytes.TrimSpace(witness)) {
		t.Fatal("restored operator evidence mismatch", string(b))
	}
	if identityResult := op("verify-staged-identity", restoredDSN, identityWitness); !bytes.Equal(bytes.TrimSpace(identityResult), bytes.TrimSpace(identityWitness)) {
		t.Fatal("restored identity operator mismatch")
	}
	restoredCfg := x.Pool.Config()
	restoredCfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(t.Context(), restoredCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	counts(t, pool, e.EventID, 1)
	retained, err := (Engine{Pool: pool}).Read(t.Context(), "a", e.EventID, e.ObservedAt)
	if err != nil {
		t.Fatal(err)
	}
	retainedBytes, err := json.Marshal(retained)
	if err != nil || !bytes.Equal(originalBytes, retainedBytes) {
		t.Fatal("retained processing evidence changed", err)
	}
	var source string
	if err = pool.QueryRow(t.Context(), "SELECT config::text FROM devices WHERE id='d'").Scan(&source); err != nil || !strings.Contains(source, "PRESERVE-SYNTHETIC") {
		t.Fatal(source, err)
	}
	var telemetryValue float64
	if err = pool.QueryRow(t.Context(), "SELECT value FROM telemetry WHERE event_id=$1", e.EventID).Scan(&telemetryValue); err != nil || telemetryValue != e.Value {
		t.Fatal(telemetryValue, err)
	}
	var serial string
	if err = pool.QueryRow(t.Context(), "SELECT serial FROM gateways WHERE id='g'").Scan(&serial); err != nil || serial != "SYNTHETIC" {
		t.Fatal(serial, err)
	}
	// NEW server uses stale route plus restored DB. It must still deny exact replay
	// unsigned without another row, proving restored denial is actually enforced.
	config.Admission.Engine.Pool = pool
	if guarded, err := NewGuardedAdmissionServer(t.Context(), config, dir); guarded != nil || !errors.Is(err, ErrGuardedAdmissionServer) {
		t.Fatal("restored denied leaf returned a guarded server", guarded, err)
	}
	t.Log("ACTUAL_PGRESTORE_GUARDED_SERVER_REFUSED_BEFORE_SERVE_OK")
	// The LEGACY constructor still returns an object; its enabled per-request
	// guards must independently refuse the restored denial over actual TLS.
	fresh, err := NewAdmissionServer(config)
	if err != nil {
		t.Fatal(err)
	}
	endpoint = startAdmissionServer(t, fresh)
	if code, body := durableRequest(t, f, endpoint, e); code != 503 || strings.Contains(string(body), "signature") {
		t.Fatal(code, string(body))
	}
	counts(t, pool, e.EventID, 1)
	// Selected authority survives the actual logical restore, including the
	// current revoked gateway, tracked+revoked sessions, absent/admin scopes and audit.
	var gatewayStatus string
	var audits, revokedSessions, scopes int
	if err = pool.QueryRow(t.Context(), "SELECT status FROM gateways WHERE id='g'").Scan(&gatewayStatus); err != nil || gatewayStatus != "revoked" {
		t.Fatal(gatewayStatus, err)
	}
	if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM audit_log WHERE action='staged.gateway_quarantined'").Scan(&audits); err != nil || audits != 1 {
		t.Fatal(audits, err)
	}
	if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM user_sessions WHERE revoked_at IS NOT NULL").Scan(&revokedSessions); err != nil || revokedSessions != 1 {
		t.Fatal(revokedSessions, err)
	}
	if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM user_customer_scope WHERE user_id='other-user'").Scan(&scopes); err != nil || scopes != 1 {
		t.Fatal(scopes, err)
	}
	// Same denial authority but LOST quarantine status: strict95 and old denial
	// witness still pass, the independently preserved identity witness MUST fail.
	exec(t, pool, "UPDATE gateways SET status='active' WHERE id='g'")
	if err = authorityschema.Inspect(t.Context(), pool, dir); err != nil {
		t.Fatal(err)
	}
	if err = authorityschema.VerifyEvidence(t.Context(), pool, dir, expected); err != nil {
		t.Fatal("denial witness semantics changed", err)
	}
	if err = authorityschema.VerifyIdentityEvidence(t.Context(), pool, dir, identityExpected); err == nil {
		t.Fatal("identity witness accepted lost quarantine")
	}
	reject := osexec.CommandContext(t.Context(), operator, "verify-staged-identity")
	reject.Env = []string{"PATH=" + os.Getenv("PATH"), "DATABASE_URL=" + restoredDSN, "MIGRATIONS_DIR=" + dir}
	reject.Stdin = bytes.NewReader(identityWitness)
	if out, err := reject.CombinedOutput(); err == nil || !strings.Contains(string(out), "continuity differs") {
		t.Fatal("actual operator accepted lost quarantine", err, string(out))
	}
	exec(t, pool, "UPDATE gateways SET status='revoked' WHERE id='g'")
	if err = authorityschema.VerifyIdentityEvidence(t.Context(), pool, dir, identityExpected); err != nil {
		t.Fatal("exact restoration mismatch", err)
	}
	t.Log("ACTUAL_SETUPCTL95_SELECTED_IDENTITY_QUARANTINE_SESSION_SCOPE_AUDIT_EMPTY_PGRESTORE_OK")
	// Demonstrate why strict shape/count alone is not sufficient: losing the row
	// while preserving epoch/catalog still passes schema, but witness MUST fail.
	exec(t, pool, "DELETE FROM admission_denied_leaf_v1")
	if err = authorityschema.Inspect(t.Context(), pool, dir); err != nil {
		t.Fatal("shape-only inspector changed", err)
	}
	if err = authorityschema.VerifyEvidence(t.Context(), pool, dir, expected); err == nil {
		t.Fatal("lost authority accepted")
	}
	cmd := osexec.CommandContext(t.Context(), operator, "verify-staged-denials")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "DATABASE_URL=" + restoredDSN, "MIGRATIONS_DIR=" + dir}
	cmd.Stdin = bytes.NewReader(witness)
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "continuity differs") {
		t.Fatal("actual operator accepted lost authority", err, string(out))
	}
	t.Log("ACTUAL_SETUPCTL95_PGDUMP_EMPTY_PGRESTORE_GUARDED_MTLS_DENIAL_CONTINUITY_OK")
	t.Log("ACTUAL_PGRESTORE_CURRENT_GATEWAY_AUTHORITY_GUARD_OK")
}
