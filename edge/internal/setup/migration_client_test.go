package setup

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/claim"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func savedMigrationKey(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	d, _ := x509.MarshalECPrivateKey(k)
	os.WriteFile(filepath.Join(dir, "identity.key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: d}), 0600)
	os.WriteFile(filepath.Join(dir, "identity.json"), []byte(`{"gateway_id":"same-gateway","ingest_token":"synthetic-only"}`), 0600)
	return dir
}
func TestMigrationRequestRetryKeepsCSRAndPrivateIdentity(t *testing.T) {
	dir := savedMigrationKey(t)
	before, _ := os.ReadFile(filepath.Join(dir, "identity.key"))
	var first string
	calls := 0
	var req MigrationCSR
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer synthetic-only" {
			t.Error("wrong identity")
		}
		json.NewDecoder(r.Body).Decode(&req)
		if first == "" {
			first = req.CSRPEM
		} else if first != req.CSRPEM {
			t.Error("CSR changed on retry")
		}
		b, _ := pem.Decode([]byte(req.CSRPEM))
		c, _ := x509.ParseCertificateRequest(b.Bytes)
		if c.Subject.CommonName != "SAVED-1" {
			t.Error("wrong serial")
		}
		local, _ := ExistingKeyCSR(dir, "SAVED-1")
		var staged MigrationRequest
		raw, _ := os.ReadFile(filepath.Join(dir, "migration-request.json"))
		json.Unmarshal(raw, &staged)
		json.NewEncoder(w).Encode(map[string]any{"id": "request-id", "key_sha256": local.KeySHA256, "csr_sha256": staged.CSRSHA256, "state": "pending", "expires_at": time.Now().Add(time.Minute)})
	}))
	defer srv.Close()
	id := &claim.Identity{GatewayID: "same-gateway", IngestToken: "synthetic-only"}
	for i := 0; i < 2; i++ {
		out, e := PrepareMigration(context.Background(), srv.Client(), srv.URL, dir, "SAVED-1", id)
		if e != nil || out.ID != "request-id" {
			t.Fatal(out, e)
		}
	}
	after, _ := os.ReadFile(filepath.Join(dir, "identity.key"))
	if calls != 2 || !bytes.Equal(before, after) {
		t.Fatal("identity changed")
	}
	if _, e := os.Stat(filepath.Join(dir, "identity.crt")); !os.IsNotExist(e) {
		t.Fatal("certificate created before approval")
	}
}
func TestMigrationRedirectDoesNotSendSavedTokenToDestination(t *testing.T) {
	dir := savedMigrationKey(t)
	hits := 0
	dst := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer dst.Close()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, dst.URL, 307) }))
	defer src.Close()
	_, e := PrepareMigration(context.Background(), src.Client(), src.URL, dir, "SAVED-1", &claim.Identity{GatewayID: "same", IngestToken: "synthetic-only"})
	if e == nil || hits != 0 {
		t.Fatal("followed redirect", e, hits)
	}
}
func TestMigrationRecoveryRestoresEveryFileAndNeverKey(t *testing.T) {
	dir := savedMigrationKey(t)
	key, _ := os.ReadFile(filepath.Join(dir, "identity.key"))
	old := map[string][]byte{"identity.crt": []byte("old-cert"), "ca.pem": []byte("old-ca"), "device-ca.pem": nil}
	for _, name := range migrationNames {
		os.WriteFile(filepath.Join(dir, name), []byte("interrupted-new"), 0600)
	}
	j, _ := json.Marshal(migrationJournal{Previous: old})
	os.WriteFile(filepath.Join(dir, "migration-install.json"), j, 0600)
	if !MigrationPending(dir) {
		t.Fatal("journal ignored")
	}
	if e := RecoverMigration(dir); e != nil {
		t.Fatal(e)
	}
	for _, name := range migrationNames {
		got, e := os.ReadFile(filepath.Join(dir, name))
		if old[name] == nil {
			if !os.IsNotExist(e) {
				t.Fatal("new file retained")
			}
		} else if !bytes.Equal(got, old[name]) {
			t.Fatal(name)
		}
	}
	got, _ := os.ReadFile(filepath.Join(dir, "identity.key"))
	if !bytes.Equal(got, key) || MigrationPending(dir) {
		t.Fatal("key changed or journal remained")
	}
	os.WriteFile(filepath.Join(dir, "migration-install.json"), []byte("corrupt"), 0600)
	if RecoverMigration(dir) == nil {
		t.Fatal("corrupt journal accepted")
	}
}
func TestMigrationLockExcludesConcurrentPairing(t *testing.T) {
	dir := savedMigrationKey(t)
	unlock, e := migrationLock(dir)
	if e != nil {
		t.Fatal(e)
	}
	if f, e := migrationLock(dir); e == nil {
		f()
		t.Fatal("concurrent operation accepted")
	}
	unlock()
	f, e := migrationLock(dir)
	if e != nil {
		t.Fatal(e)
	}
	f()
}

func TestMigrationCommittedRecoveryDoesNotRollBackAndBadBundleDoesNotWrite(t *testing.T) {
	dir := savedMigrationKey(t)
	j := migrationJournal{Previous: map[string][]byte{"identity.crt": []byte("old"), "ca.pem": nil, "device-ca.pem": nil}, Committed: true}
	for _, name := range migrationNames {
		os.WriteFile(filepath.Join(dir, name), []byte("new"), 0600)
	}
	raw, _ := json.Marshal(j)
	os.WriteFile(filepath.Join(dir, "migration-install.json"), raw, 0600)
	if e := RecoverMigration(dir); e != nil {
		t.Fatal(e)
	}
	for _, name := range migrationNames {
		got, _ := os.ReadFile(filepath.Join(dir, name))
		if string(got) != "new" {
			t.Fatal("committed install rolled back")
		}
	}
	before, _ := os.ReadFile(filepath.Join(dir, "identity.key"))
	if e := InstallMigration(context.Background(), dir, "SAVED-1", "same-gateway", MigrationBundle{ClientCert: "invalid"}, nil); e == nil {
		t.Fatal("invalid certificate accepted")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "identity.key"))
	if !bytes.Equal(before, after) || MigrationPending(dir) {
		t.Fatal("invalid bundle changed files")
	}
}

func TestExpiredMigrationRequestArchivesOnlyPublicStateBeforeExplicitRetry(t *testing.T) {
	dir := savedMigrationKey(t)
	keyBefore, _ := os.ReadFile(filepath.Join(dir, "identity.key"))
	csr, e := ExistingKeyCSR(dir, "SAVED-1")
	if e != nil {
		t.Fatal(e)
	}
	saved := MigrationRequest{MigrationCSR: csr, ID: "old-request", ExpiresAt: time.Now().Add(-time.Second)}
	raw, _ := json.Marshal(saved)
	path := filepath.Join(dir, "migration-request.json")
	os.WriteFile(path, raw, 0600)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer srv.Close()
	_, e = PrepareMigration(context.Background(), srv.Client(), srv.URL, dir, "SAVED-1", &claim.Identity{GatewayID: "same", IngestToken: "synthetic-only"})
	if e == nil || calls != 0 {
		t.Fatal("expired request was sent")
	}
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("expired public request not archived")
	}
	archived, _ := filepath.Glob(path + ".expired-*")
	if len(archived) != 1 {
		t.Fatal("missing public archive")
	}
	keyAfter, _ := os.ReadFile(filepath.Join(dir, "identity.key"))
	if !bytes.Equal(keyBefore, keyAfter) {
		t.Fatal("saved key replaced")
	}
}
