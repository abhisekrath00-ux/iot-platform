package ingestprocessing

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/url"
	"os"
	osExec "os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The child uses only a synthetic isolated DB sink, never a vendor or device.
func connectChild(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	return pgxpool.New(ctx, dsn)
}
func TestProcessingExecuteChild(t *testing.T) {
	dsn := os.Getenv("SYNTHETIC_PROCESSING_CHILD_DB")
	if dsn == "" {
		t.Skip("subprocess helper")
	}
	p, err := connectChild(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var e Envelope
	if err = json.NewDecoder(os.Stdin).Decode(&e); err != nil {
		t.Fatal(err)
	}
	x := Engine{Pool: p, Enabled: true}
	if os.Getenv("SYNTHETIC_PROCESSING_CHILD_STOP") == "after-claim" {
		if _, err = x.startAttempt(t.Context(), e.TenantID, e.EventID, e.ObservedAt, syntheticSender(func(context.Context, Envelope, Intent) error { return nil })); err != nil {
			t.Fatal(err)
		}
		fmt.Println("SYNTHETIC_CHECKPOINT after-claim")
		if err := syscall.Kill(os.Getpid(), syscall.SIGSTOP); err != nil {
			t.Fatal(err)
		}
		<-t.Context().Done()
		return
	}
	s := syntheticSender(func(ctx context.Context, got Envelope, i Intent) error {
		if _, err := p.Exec(ctx, `INSERT INTO synthetic_provider_receipts(event_id,sequence) VALUES($1,$2)`, got.EventID, i.Sequence); err != nil {
			return err
		}
		fmt.Println("SYNTHETIC_CHECKPOINT after-first-provider-acceptance")
		if err := syscall.Kill(os.Getpid(), syscall.SIGSTOP); err != nil {
			t.Fatal(err)
		}
		<-t.Context().Done()
		return nil
	})
	if err = x.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, s); err != nil {
		t.Fatal(err)
	}
}
func TestProcessingActualKilledAttemptNeverResends(t *testing.T) {
	for _, mode := range []string{"after-claim", "after-first-provider-acceptance"} {
		t.Run(mode, func(t *testing.T) {
			x, e := fixture(t)
			ctx := t.Context()
			mustAdmit(t, x, e)
			execSQL := `CREATE TABLE synthetic_provider_receipts(event_id TEXT,sequence INT)`
			exec(t, x.Pool, execSQL)
			u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
			if err != nil {
				t.Fatal(err)
			}
			u.Path = "/" + x.Pool.Config().ConnConfig.Database
			cmd := osExec.Command(os.Args[0], "-test.run=^TestProcessingExecuteChild$")
			cmd.Env = append(os.Environ(), "SYNTHETIC_PROCESSING_CHILD_DB="+u.String(), "SYNTHETIC_PROCESSING_CHILD_STOP="+mode)
			b, _ := json.Marshal(e)
			cmd.Stdin = strings.NewReader(string(b))
			cmd.Stderr = os.Stderr
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer cmd.Process.Kill()
			ready := make(chan bool, 1)
			go func() {
				scan := bufio.NewScanner(stdout)
				for scan.Scan() {
					if strings.HasPrefix(scan.Text(), "SYNTHETIC_CHECKPOINT "+mode) {
						ready <- true
						return
					}
				}
				ready <- false
			}()
			select {
			case ok := <-ready:
				if !ok {
					t.Fatal("child checkpoint missing")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("child checkpoint timeout")
			}
			// The checkpoint is emitted before self-stop. Verify the actual process
			// is stopped rather than racing a normally exiting helper.
			stopped := false
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				b, readErr := os.ReadFile(fmt.Sprintf("/proc/%d/status", cmd.Process.Pid))
				if readErr == nil && strings.Contains(string(b), "State:\tT") {
					stopped = true
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if !stopped {
				t.Fatal("child did not enter actual stopped state")
			}
			if err = cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			var exit *osExec.ExitError
			if !errors.As(err, &exit) {
				t.Fatal("child did not die", err)
			}
			status, ok := exit.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
				t.Fatal(status, err)
			}
			record, err := x.Read(ctx, e.TenantID, e.EventID, e.ObservedAt)
			if err != nil || record.State != "executing" || record.Quarantine != "attempt_started_no_takeover_or_retry" {
				t.Fatal(record, err)
			}
			var receipts int
			if err = x.Pool.QueryRow(ctx, "SELECT count(*) FROM synthetic_provider_receipts").Scan(&receipts); err != nil {
				t.Fatal(err)
			}
			want := 0
			if mode == "after-first-provider-acceptance" {
				want = 1
			}
			if receipts != want {
				t.Fatal(receipts, want)
			}
			calls := 0
			s := syntheticSender(func(context.Context, Envelope, Intent) error { calls++; return nil })
			if err = x.ExecuteOnce(ctx, e.TenantID, e.EventID, e.ObservedAt, s); !errors.Is(err, ErrState) {
				t.Fatal(err)
			}
			if a, err := x.Admit(ctx, e); err != nil || a.New || a.State != "executing" {
				t.Fatal(a, err)
			}
			if calls != 0 {
				t.Fatal("external attempt was rearmed", calls)
			}
		})
	}
}
