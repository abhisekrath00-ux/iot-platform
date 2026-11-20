package ingestprocessing

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/authorityschema"
)

// Only short-lived synthetic test identities. These bytes are written mode0600
// inside t.TempDir, never output, published, sent to a provider or used by startup.
type runtimeProcessFixture struct {
	CA, ServerDER, ServerKey, ClientDER, ClientKey []byte
	Signing                                        ReceiptSigning
	Original                                       authorityschema.IdentityEvidence
	Reading                                        Envelope
}

func runtimeProcessKeys(t *testing.T, f httpMTLS, e Envelope, original authorityschema.IdentityEvidence) runtimeProcessFixture {
	t.Helper()
	sk, err := x509.MarshalPKCS8PrivateKey(f.server.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	ck, err := x509.MarshalPKCS8PrivateKey(f.client.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	return runtimeProcessFixture{CA: f.ca, ServerDER: f.server.Certificate[0], ServerKey: sk, ClientDER: f.client.Certificate[0], ClientKey: ck, Signing: signing(t), Original: original, Reading: e}
}
func (p runtimeProcessFixture) tls(t *testing.T) httpMTLS {
	t.Helper()
	roots := x509.NewCertPool()
	ca, err := x509.ParseCertificate(p.CA)
	if err != nil {
		t.Fatal(err)
	}
	roots.AddCert(ca)
	sk, err := x509.ParsePKCS8PrivateKey(p.ServerKey)
	if err != nil {
		t.Fatal(err)
	}
	ck, err := x509.ParsePKCS8PrivateKey(p.ClientKey)
	if err != nil {
		t.Fatal(err)
	}
	return httpMTLS{ca: p.CA, roots: roots, server: tls.Certificate{Certificate: [][]byte{p.ServerDER}, PrivateKey: sk}, client: tls.Certificate{Certificate: [][]byte{p.ClientDER}, PrivateKey: ck}}
}
func runtimeProcessConfig(t *testing.T, x Engine, p runtimeProcessFixture) StagedGatewayRuntimeConfig {
	t.Helper()
	f := p.tls(t)
	admission := guardedConfig(t, f, x)
	admission.Admission.Signing = p.Signing
	ec := guardedExecutionConfig(t, x, f)
	return StagedGatewayRuntimeConfig{Enabled: true, Admission: admission, Management: quarantineServerConfig(f, x), MigrationDirectory: "../../migrations", SelectedAuthority: p.Original, Execution: &ec}
}
func TestRuntimeProcessingChild(t *testing.T) {
	path := os.Getenv("SYNTHETIC_RUNTIME_FIXTURE")
	if path == "" {
		t.Skip("isolated child helper")
	}
	p, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture runtimeProcessFixture
	if err = json.Unmarshal(p, &fixture); err != nil {
		t.Fatal("invalid private test fixture")
	}
	pool, err := connectChild(t.Context(), os.Getenv("SYNTHETIC_RUNTIME_DB"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	x := Engine{Enabled: true, Pool: pool}
	s, err := NewStagedGatewayRuntime(t.Context(), runtimeProcessConfig(t, x, fixture))
	if err != nil {
		t.Fatal(err)
	}
	endpoint, _, cancel, _ := startGatewayRuntime(t, s)
	defer cancel()
	if code, _ := durableRequest(t, fixture.tls(t), endpoint, fixture.Reading); code != 200 {
		t.Fatal(code)
	}
	mode := os.Getenv("SYNTHETIC_RUNTIME_STOP")
	err = s.ExecuteOnce(t.Context(), fixture.Reading.TenantID, fixture.Reading.EventID, fixture.Reading.ObservedAt, syntheticSender(func(ctx context.Context, e Envelope, i Intent) error {
		if i.Sequence != 0 {
			t.Fatal("second occurrence entered")
		}
		rec, err := x.Read(ctx, e.TenantID, e.EventID, e.ObservedAt)
		if err != nil || rec.State != "executing" {
			t.Fatal("claim not retained", err)
		}
		if mode == "after-first-acceptance" {
			if _, err = pool.Exec(ctx, "INSERT INTO synthetic_runtime_receipts(event_id,sequence) VALUES($1,$2)", e.EventID, i.Sequence); err != nil {
				t.Fatal(err)
			}
		}
		fmt.Println("SYNTHETIC_RUNTIME_CHECKPOINT " + mode)
		if err = syscall.Kill(os.Getpid(), syscall.SIGSTOP); err != nil {
			t.Fatal(err)
		}
		<-ctx.Done()
		return ctx.Err()
	}))
	t.Fatal("child expected to be killed, not finish", err)
}
func TestRuntimeActualKilledBatchReconstructsWithoutResend(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"after-claim-before-effect", "after-first-acceptance"} {
		t.Run(mode, func(t *testing.T) {
			x, e := denialFixture(t)
			f := httpTLSFixture(t)
			bindGatewayLeaf(t, x, f)
			exec(t, x.Pool, "CREATE TABLE synthetic_runtime_receipts(event_id text,sequence integer)")
			original, err := authorityschema.InspectIdentityEvidence(t.Context(), x.Pool, "../../migrations")
			if err != nil {
				t.Fatal(err)
			}
			spec := runtimeProcessKeys(t, f, e, original)
			b, err := json.Marshal(spec)
			if err != nil {
				t.Fatal("fixture encoding")
			}
			path := filepath.Join(t.TempDir(), "synthetic-private-runtime.json")
			if err = os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
			if err != nil {
				t.Fatal(err)
			}
			u.Path = "/" + x.Pool.Config().ConnConfig.Database
			child := osexec.Command(os.Args[0], "-test.run=^TestRuntimeProcessingChild$")
			child.Env = append(os.Environ(), "SYNTHETIC_RUNTIME_FIXTURE="+path, "SYNTHETIC_RUNTIME_DB="+u.String(), "SYNTHETIC_RUNTIME_STOP="+mode)
			child.Stderr = os.Stderr
			out, err := child.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err = child.Start(); err != nil {
				t.Fatal(err)
			}
			defer child.Process.Kill()
			checkpoint := make(chan bool, 1)
			go func() {
				scan := bufio.NewScanner(out)
				for scan.Scan() {
					if scan.Text() == "SYNTHETIC_RUNTIME_CHECKPOINT "+mode {
						checkpoint <- true
						return
					}
				}
				checkpoint <- false
			}()
			select {
			case ok := <-checkpoint:
				if !ok {
					t.Fatal("checkpoint missing")
				}
			case <-time.After(12 * time.Second):
				t.Fatal("checkpoint timed out")
			}
			stopped := false
			deadline := time.Now().Add(4 * time.Second)
			for time.Now().Before(deadline) {
				status, readErr := os.ReadFile(fmt.Sprintf("/proc/%d/status", child.Process.Pid))
				if readErr == nil && strings.Contains(string(status), "State:\tT") {
					stopped = true
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if !stopped {
				t.Fatal("not actually stopped")
			}
			if err = child.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			err = child.Wait()
			var died *osexec.ExitError
			if !errors.As(err, &died) {
				t.Fatal("child did not die", err)
			}
			status, ok := died.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
				t.Fatal("not SIGKILL")
			}
			assertState(t, x, e, "executing")
			historyCount(t, x, 1, "processing_started")
			historyCount(t, x, 0, "processing_completed")
			rec, err := x.Read(t.Context(), e.TenantID, e.EventID, e.ObservedAt)
			if err != nil || rec.Quarantine != "attempt_started_no_takeover_or_retry" || rec.AttemptID == "" {
				t.Fatal("lost uncertainty", err)
			}
			var receipts int
			if err = x.Pool.QueryRow(t.Context(), "SELECT count(*) FROM synthetic_runtime_receipts").Scan(&receipts); err != nil {
				t.Fatal(err)
			}
			want := 0
			if mode == "after-first-acceptance" {
				want = 1
			}
			if receipts != want {
				t.Fatal(receipts, want)
			}
			// Independent original selected authority witness and SAME ephemeral TLS /
			// receipt-signing keys, not a baseline regenerated from the crashed DB.
			s, err := NewStagedGatewayRuntime(t.Context(), runtimeProcessConfig(t, x, spec))
			if err != nil {
				t.Fatal("reconstruct", err)
			}
			endpoint, _, cancel, done := startGatewayRuntime(t, s)
			defer cancel()
			code, body := durableRequest(t, f, endpoint, e)
			if code != 200 {
				t.Fatal("same-input reconciliation failed", code)
			}
			receipt := verifySigned(t, body, spec.Signing)
			expected, err := admissionReceipt(Admission{State: "executing", Plan: rec.Plan})
			if err != nil || receipt != expected {
				t.Fatal("signed reconciliation lost original binding or changed state", err)
			}
			calls := 0
			sender := syntheticSender(func(context.Context, Envelope, Intent) error { calls++; return nil })
			if err = s.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, sender); !errors.Is(err, ErrState) || calls != 0 {
				t.Fatal("rearmed killed batch", err, calls)
			}
			fresh, err := x.Read(t.Context(), e.TenantID, e.EventID, e.ObservedAt)
			if err != nil || fresh.AttemptID != rec.AttemptID || fresh.State != "executing" {
				t.Fatal("attempt replaced", err)
			}
			counts(t, x.Pool, e.EventID, 1)
			var after int
			if err = x.Pool.QueryRow(t.Context(), "SELECT count(*) FROM synthetic_runtime_receipts").Scan(&after); err != nil || after != want {
				t.Fatal("receipt count changed after restart", err, after, want)
			}
			// A separate durable denial after restart blocks admission too, rather than
			// allowing a new input identity to bypass uncertainty.
			if err = x.DenyLeaf(t.Context(), sha256.Sum256(f.client.Certificate[0])); err != nil {
				t.Fatal(err)
			}
			if code, _ := durableRequest(t, f, endpoint, e); code != 503 {
				t.Fatal(code)
			}
			cancel()
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			if err = s.WaitDrained(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Log("ACTUAL_COMPOSED_RUNTIME_SIGSTOP_SIGKILL_ORIGINAL_WITNESS_SAME_KEYS_MTLS_RECONCILE_EXECUTING_NO_TAKEOVER_NO_RESEND_OK")
}
