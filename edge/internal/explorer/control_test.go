package explorer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func watchFixture(t *testing.T, dir string, apply func(string) (string, time.Time, error)) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); RunControl(ctx, dir, apply) }()
	t.Cleanup(func() { cancel(); <-done })
}
func TestLocalControlFreshExplicitReceiptNoReplay(t *testing.T) {
	dir := t.TempDir()
	var calls atomic.Int32
	watchFixture(t, dir, func(action string) (string, time.Time, error) {
		calls.Add(1)
		var receipt ControlReceipt
		if readControlJSON(filepath.Join(controlDir(dir), "receipt.json"), &receipt) != nil || receipt.State != "processing" {
			t.Error("effect lacked persisted processing fence")
		}
		if action == "close" {
			return "", time.Time{}, nil
		}
		return "http://127.0.0.1:12345", time.Now().Add(time.Hour), nil
	})
	if _, err := os.Stat(controlDir(dir)); !os.IsNotExist(err) {
		t.Fatal("idle watcher created state")
	}
	receipt, err := RequestControl(context.Background(), dir, "open")
	if err != nil || receipt.URL != "http://127.0.0.1:12345" || receipt.State != "complete" || calls.Load() != 1 {
		t.Fatal(receipt, err, calls.Load())
	}
	// Put the same original request back. A durable completed receipt fences it.
	req := ControlRequest{ID: receipt.ID, Action: "open", CreatedAt: time.Now()}
	if err := writeControlJSON(filepath.Join(controlDir(dir), "request.json"), req); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatal("completed request replayed")
	}
	if _, err := RequestControl(context.Background(), dir, "close"); err != nil || calls.Load() != 2 {
		t.Fatal("close failed", err, calls.Load())
	}
}
func TestLocalControlInterruptedAttemptAndStaleRequestsNeverApply(t *testing.T) {
	for _, kind := range []string{"processing", "stale", "future", "unsupported"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			os.Mkdir(controlDir(dir), 0700)
			id := uuid.NewString()
			req := ControlRequest{ID: id, Action: "open", CreatedAt: time.Now()}
			switch kind {
			case "processing":
				writeControlJSON(filepath.Join(controlDir(dir), "receipt.json"), ControlReceipt{ID: id, State: "processing"})
			case "stale":
				req.CreatedAt = time.Now().Add(-time.Minute)
			case "future":
				req.CreatedAt = time.Now().Add(time.Minute)
			case "unsupported":
				req.Action = "write-device"
			}
			writeControlJSON(filepath.Join(controlDir(dir), "request.json"), req)
			var calls atomic.Int32
			watchFixture(t, dir, func(string) (string, time.Time, error) { calls.Add(1); return "", time.Time{}, nil })
			deadline := time.Now().Add(2 * time.Second)
			var receipt ControlReceipt
			for time.Now().Before(deadline) {
				readControlJSON(filepath.Join(controlDir(dir), "receipt.json"), &receipt)
				if receipt.ID == id && receipt.Error != "" {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if calls.Load() != 0 || receipt.Error == "" || (kind == "processing" && receipt.State != "uncertain") {
				t.Fatal("invalid/interrupted request applied or hidden", receipt, calls.Load())
			}
		})
	}
}
func TestLocalControlBusyTimeoutCleanupAndStaleLock(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := RequestControl(ctx, dir, "open"); done <- err }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(controlDir(dir), "request.lock", "owner")); err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := RequestControl(context.Background(), dir, "close"); err == nil || !strings.Contains(err.Error(), "active") {
		t.Fatal("simultaneous CLI did not refuse", err)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("unconfirmed request called success")
	}
	if _, err := os.Stat(filepath.Join(controlDir(dir), "request.json")); !os.IsNotExist(err) {
		t.Fatal("timed out request retained")
	}
	if _, err := os.Stat(filepath.Join(controlDir(dir), "request.lock")); !os.IsNotExist(err) {
		t.Fatal("CLI lock retained")
	}
	lock := filepath.Join(controlDir(dir), "request.lock")
	os.Mkdir(lock, 0700)
	os.WriteFile(filepath.Join(lock, "owner"), []byte("abandoned"), 0600)
	old := time.Now().Add(-time.Minute)
	os.Chtimes(lock, old, old)
	watchFixture(t, dir, func(string) (string, time.Time, error) {
		return "http://127.0.0.1:12345", time.Now().Add(time.Hour), nil
	})
	if _, err := RequestControl(context.Background(), dir, "open"); err != nil {
		t.Fatal("abandoned CLI lock not recovered", err)
	}
}
func TestLocalControlBoundedStrictFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "request.json")
	for _, body := range []string{`{"id":"x","action":"open","write":true}`, `{} {}`, strings.Repeat(" ", 4097)} {
		os.WriteFile(path, []byte(body), 0600)
		var req ControlRequest
		if err := readControlJSON(path, &req); err == nil {
			t.Fatal("malformed control accepted")
		}
	}
}
