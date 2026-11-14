package explorer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

type ControlRequest struct {
	ID        string    `json:"id"`
	Action    string    `json:"action"`
	CreatedAt time.Time `json:"created_at"`
}
type ControlReceipt struct {
	ID        string    `json:"id"`
	State     string    `json:"state"`
	URL       string    `json:"url,omitempty"`
	Error     string    `json:"error,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

func controlDir(dir string) string { return filepath.Join(dir, "explorer-control") }
func writeControlJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".explorer-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	return replaceControlFile(f.Name(), path)
}
func readControlJSON(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return err
	}
	if len(b) > 4096 {
		return fmt.Errorf("control file too large")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("one control object required")
	}
	return nil
}

// RequestControl is an explicit desktop/operator request to an already-running
// agent. It never starts/restarts the service, edits config, opens a device,
// retries a request, or assumes a listener exists because a port answers.
// The per-request receipt is the proof used before opening the local browser.
func RequestControl(ctx context.Context, dir, action string) (ControlReceipt, error) {
	if action != "open" && action != "close" {
		return ControlReceipt{}, fmt.Errorf("choose open or close")
	}
	req := ControlRequest{ID: uuid.NewString(), Action: action, CreatedAt: time.Now().UTC()}
	dir = controlDir(dir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return ControlReceipt{}, err
	}
	lock := filepath.Join(dir, "request.lock")
	if err := os.Mkdir(lock, 0700); err != nil {
		// A CLI invocation lasts at most eight seconds. An abandoned lock may be
		// recovered after thirty; its old request is already outside the 15s fence.
		st, e := os.Lstat(lock)
		if e != nil || !st.IsDir() || time.Since(st.ModTime()) < 30*time.Second {
			return ControlReceipt{}, fmt.Errorf("another explorer request is active; wait briefly")
		}
		quarantine := lock + "-abandoned-" + req.ID
		if err := os.Rename(lock, quarantine); err != nil {
			return ControlReceipt{}, err
		}
		os.RemoveAll(quarantine)
		if err := os.Mkdir(lock, 0700); err != nil {
			return ControlReceipt{}, fmt.Errorf("another explorer request is active")
		}
	}
	owner := filepath.Join(lock, "owner")
	if err := os.WriteFile(owner, []byte(req.ID), 0600); err != nil {
		os.Remove(lock)
		return ControlReceipt{}, err
	}
	defer func() {
		b, e := os.ReadFile(owner)
		if e == nil && string(b) == req.ID {
			os.Remove(owner)
			os.Remove(lock)
		}
	}()
	path := filepath.Join(dir, "request.json")
	if err := writeControlJSON(path, req); err != nil {
		return ControlReceipt{}, err
	}
	defer func() {
		var current ControlRequest
		if readControlJSON(path, &current) == nil && current.ID == req.ID {
			os.Remove(path)
		}
	}()
	timeout, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		var receipt ControlReceipt
		if readControlJSON(filepath.Join(dir, "receipt.json"), &receipt) == nil && receipt.ID == req.ID {
			if receipt.State == "complete" || receipt.State == "uncertain" {
				if receipt.Error != "" {
					return receipt, fmt.Errorf("%s", receipt.Error)
				}
				return receipt, nil
			}
		}
		select {
		case <-timeout.Done():
			return ControlReceipt{}, fmt.Errorf("no confirmation from the running agent; no automatic retry. Check service status or explicitly Close explorer before trying again")
		case <-tick.C:
		}
	}
}

// RunControl watches only the protected local state directory. No listener is
// bound until an explicit, fresh open request. An effect is marked processing
// on disk before it happens; an interrupted attempt is never replayed.
func RunControl(ctx context.Context, dir string, apply func(string) (string, time.Time, error)) {
	dir = controlDir(dir)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	last := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			var req ControlRequest
			if readControlJSON(filepath.Join(dir, "request.json"), &req) != nil || req.ID == last {
				continue
			}
			if _, err := uuid.Parse(req.ID); err != nil {
				continue
			}
			last = req.ID
			receipt := ControlReceipt{ID: req.ID, State: "complete"}
			now := time.Now()
			var previous ControlReceipt
			if readControlJSON(filepath.Join(dir, "receipt.json"), &previous) == nil && previous.ID == req.ID {
				if previous.State == "processing" {
					previous.State = "uncertain"
					previous.Error = "an earlier explorer request was interrupted; it was not retried. Check or explicitly close the explorer"
					writeControlJSON(filepath.Join(dir, "receipt.json"), previous)
				}
				continue
			}
			if req.Action != "open" && req.Action != "close" || req.CreatedAt.After(now.Add(2*time.Second)) || now.Sub(req.CreatedAt) > 15*time.Second {
				receipt.Error = "expired or invalid explorer request; nothing was changed"
				writeControlJSON(filepath.Join(dir, "receipt.json"), receipt)
				continue
			}
			if err := writeControlJSON(filepath.Join(dir, "receipt.json"), ControlReceipt{ID: req.ID, State: "processing"}); err != nil {
				continue
			}
			url, expiry, err := apply(req.Action)
			receipt.URL = url
			receipt.ExpiresAt = expiry
			if err != nil {
				receipt.Error = err.Error()
			}
			// Failure to persist the result deliberately leaves processing uncertainty.
			writeControlJSON(filepath.Join(dir, "receipt.json"), receipt)
		}
	}
}
