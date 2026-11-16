package ingestprocessing

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func signing(t *testing.T) ReceiptSigning {
	t.Helper()
	_, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	return ReceiptSigning{Enabled: true, Audience: "isolated-synthetic-deployment", PrivateKey: key}
}
func verifySigned(t *testing.T, b []byte, c ReceiptSigning) AdmissionReceipt {
	t.Helper()
	var w signedAdmissionWire
	if json.Unmarshal(b, &w) != nil || !ed25519.Verify(c.PrivateKey.Public().(ed25519.PublicKey), append([]byte(receiptDomain), w.Payload...), w.Signature) {
		t.Fatal("invalid signature")
	}
	var p signedAdmissionPayload
	if json.Unmarshal(w.Payload, &p) != nil || p.Version != 1 || p.Audience != c.Audience {
		t.Fatal("invalid payload")
	}
	return p.Receipt
}
func TestSignedReceiptConfigurationRefusesBeforePool(t *testing.T) {
	c := signing(t)
	for _, mode := range []string{"disabled", "audience", "key", "inconsistent", "context"} {
		t.Run(mode, func(t *testing.T) {
			bad := c
			bad.PrivateKey = append(ed25519.PrivateKey(nil), c.PrivateKey...)
			ctx := t.Context()
			switch mode {
			case "disabled":
				bad.Enabled = false
			case "audience":
				bad.Audience = "with space"
			case "key":
				bad.PrivateKey = bad.PrivateKey[:10]
			case "inconsistent":
				bad.PrivateKey[50] ^= 1
			case "context":
				ctx = nil
			}
			if b, e := (Engine{Enabled: true}).AdmitSignedMQTT(ctx, "", nil, bad); b != nil || !errors.Is(e, ErrReceiptSigning) {
				t.Fatal(len(b), e)
			}
		})
	}
	if b, e := (Engine{}).AdmitSignedMQTT(t.Context(), "", nil, c); b != nil || !errors.Is(e, ErrDisabled) {
		t.Fatal(len(b), e)
	}
}
func TestSignedReceiptCommittedPGReplayAndEdge(t *testing.T) {
	x, e := fixture(t)
	c := signing(t)
	wire, err := x.AdmitSignedMQTT(t.Context(), "t/a/g/g/telemetry", wirePayload(t, e), c)
	if err != nil {
		t.Fatal(err)
	}
	r := verifySigned(t, wire, c)
	if !r.New || r.State != "pending" {
		t.Fatal(r)
	}
	counts(t, x.Pool, e.EventID, 1)
	historyCount(t, x, 0, "processing_started")
	for _, v := range []string{"target", "synthetic@example.invalid", "changed@example.invalid", "attempt_id", "message", "plan"} {
		if strings.Contains(string(wire), v) {
			t.Fatal("private disclosure", v)
		}
	}
	exec(t, x.Pool, `UPDATE notification_channels SET target='changed@example.invalid' WHERE id='c'`)
	var replayWire []byte
	for _, state := range []string{"pending", "executing", "outcome_unknown", "completed"} {
		if state != "pending" {
			exec(t, x.Pool, `UPDATE ingest_processing_v1 SET state=$1,attempt_id='12345678-1234-1234-1234-123456789012'`, state)
		}
		b, err := x.AdmitSignedMQTT(t.Context(), "t/a/g/g/telemetry", wirePayload(t, e), c)
		if err != nil {
			t.Fatal(err)
		}
		replayWire = append([]byte(nil), b...)
		got := verifySigned(t, b, c)
		want := r
		want.State = state
		want.New = false
		if got != want {
			t.Fatal(got, want)
		}
	}
	changed := e
	changed.Value++
	if b, err := x.AdmitSignedMQTT(t.Context(), "t/a/g/g/telemetry", wirePayload(t, changed), c); b != nil || err == nil {
		t.Fatal("conflict signed", len(b), err)
	}
	exec(t, x.Pool, `CREATE FUNCTION signed_refusal() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'synthetic refusal'; END $$ LANGUAGE plpgsql; CREATE TRIGGER signed_refusal BEFORE INSERT ON telemetry FOR EACH ROW EXECUTE FUNCTION signed_refusal()`)
	fresh := e
	fresh.EventID = "refused"
	if b, err := x.AdmitSignedMQTT(t.Context(), "t/a/g/g/telemetry", wirePayload(t, fresh), c); b != nil || err == nil {
		t.Fatal("failed insert signed", len(b), err)
	}
	counts(t, x.Pool, "refused", 0)
	// Actual server-produced signed bytes go through the independent edge module,
	// not a recreated server verifier or a handwritten claim of compatibility.
	// The temporary bridge contains only synthetic public key/input/signed evidence.
	data, err := json.Marshal(struct {
		PublicKey  []byte
		Audience   string
		Reading    Envelope
		Wire       []byte
		ReplayWire []byte
	}{c.PrivateKey.Public().(ed25519.PublicKey), c.Audience, e, wire, replayWire})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "synthetic-public-evidence.json")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	cmd := osexec.CommandContext(ctx, "go", "test", "./internal/admissionoutbox", "-run", "^TestServerSignedFixture$", "-count=1", "-v")
	cmd.Dir = filepath.Join("..", "..", "..", "edge")
	cmd.Env = append(os.Environ(), "TEST_SERVER_SIGNED_RECEIPT="+path)
	out, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(out, []byte("SERVER_SIGNED_ATOMIC_RECONCILIATION_OK")) {
		t.Fatalf("edge bridge failed: %v\n%s", err, out)
	}
	t.Log("SERVER_SIGNED_ATOMIC_RECONCILIATION_OK")
	if !bytes.Contains(out, []byte("SERVER_SIGNED_EXPLICIT_REPLAY_PUBLISHER_OK")) {
		t.Fatal("publisher bridge did not run", string(out))
	}
	t.Log("SERVER_SIGNED_EXPLICIT_REPLAY_PUBLISHER_OK")
	if !bytes.Contains(out, []byte("SERVER_SIGNED_MTLS_EXPLICIT_REPLAY_OK")) {
		t.Fatal("mTLS bridge did not run", string(out))
	}
	t.Log("SERVER_SIGNED_MTLS_EXPLICIT_REPLAY_OK")
}
