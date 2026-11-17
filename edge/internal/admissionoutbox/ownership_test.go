package admissionoutbox

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublisherOwnershipAcrossInstancesAndDrain(t *testing.T) {
	cfg := config(t, 2)
	s := openStore(t, cfg)
	it := mustPut(t, s, reading())
	other := openStore(t, cfg)
	c, k := trust(t)
	entered := make(chan struct{})
	finish := make(chan struct{})
	done := make(chan error, 1)
	first := publisher(t, s, c, func(ctx context.Context, item Item) ([]byte, error) {
		close(entered)
		<-finish
		return nil, errors.New("synthetic uncertain exchange")
	}, 1)
	go func() { _, e := first.RunOnce(t.Context()); done <- e }()
	<-entered
	var calls atomic.Int32
	second := publisher(t, other, c, func(ctx context.Context, item Item) ([]byte, error) {
		calls.Add(1)
		r := receipt(item)
		r.New = false
		return token(t, r, c, k), nil
	}, 1)
	assertBusy := func() {
		t.Helper()
		r, e := second.RunOnce(t.Context())
		if !errors.Is(e, ErrOwnership) || r.Attempted != 0 || calls.Load() != 0 {
			t.Fatal(r, e, calls.Load())
		}
	}
	assertBusy()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if e := first.Close(ctx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	// A timed-out Close cannot release ownership while an Exchange is running.
	assertBusy()
	// Input writes remain independent from the held ownership transaction.
	next := reading()
	next.EventID = "new-during-publish"
	mustPut(t, other, next)
	close(finish)
	if e := <-done; e == nil {
		t.Fatal("missing uncertainty")
	}
	rows, e := s.Next(t.Context(), 2)
	if e != nil || len(rows) != 2 || rows[0].InputSHA256 != it.InputSHA256 {
		t.Fatal(rows, e)
	}
	result, e := second.RunOnce(t.Context())
	if e != nil || result.Reconciled != 1 || calls.Load() != 1 {
		t.Fatal(result, e, calls.Load())
	}
}

func TestPublisherOwnershipKilledProcessRetainsInput(t *testing.T) {
	if path := os.Getenv("TEST_OWNER_CHILD_PATH"); path != "" {
		cfg := Config{Enabled: true, Path: path, Capacity: 2}
		s := openStore(t, cfg)
		c, _ := trust(t)
		p := publisher(t, s, c, func(ctx context.Context, it Item) ([]byte, error) {
			if e := os.WriteFile(path+".entered", it.Payload, 0600); e != nil {
				t.Fatal(e)
			}
			select {}
		}, 1)
		if _, e := p.RunOnce(t.Context()); e != nil {
			t.Fatal(e)
		}
		return
	}
	cfg := config(t, 2)
	s := openStore(t, cfg)
	it := mustPut(t, s, reading())
	child := exec.Command(os.Args[0], "-test.run=^TestPublisherOwnershipKilledProcessRetainsInput$")
	child.Env = append(os.Environ(), "TEST_OWNER_CHILD_PATH="+cfg.Path)
	var output bytes.Buffer
	child.Stdout = &output
	child.Stderr = &output
	if e := child.Start(); e != nil {
		t.Fatal(e)
	}
	waited := false
	defer func() {
		if !waited {
			child.Process.Kill()
			child.Wait()
		}
	}()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if b, e := os.ReadFile(cfg.Path + ".entered"); e == nil {
			if !bytes.Equal(b, it.Payload) {
				t.Fatal("child changed input")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child never entered", output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	c, k := trust(t)
	calls := 0
	p := publisher(t, s, c, func(ctx context.Context, sent Item) ([]byte, error) {
		calls++
		if !bytes.Equal(sent.Payload, it.Payload) {
			t.Fatal("identity changed")
		}
		r := receipt(sent)
		r.New = false
		return token(t, r, c, k), nil
	}, 1)
	result, e := p.RunOnce(t.Context())
	if !errors.Is(e, ErrOwnership) || result.Attempted != 0 || calls != 0 {
		t.Fatal(result, e, calls)
	}
	if e = child.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	if e = child.Wait(); e == nil {
		t.Fatal("child was not killed")
	}
	waited = true
	rows, e := s.Next(t.Context(), 2)
	if e != nil || len(rows) != 1 || !bytes.Equal(rows[0].Payload, it.Payload) {
		t.Fatal(rows, e)
	}
	// Owner death releases the guard, but does not automatically send or delete.
	if calls != 0 {
		t.Fatal(calls)
	}
	result, e = p.RunOnce(t.Context())
	if e != nil || result.Reconciled != 1 || calls != 1 {
		t.Fatal(result, e, calls)
	}
	t.Log("PUBLISHER_PROCESS_KILL_OWNER_RELEASE_SAME_INPUT_OK")
}

func TestPublisherOwnershipSchemaAndAlias(t *testing.T) {
	for _, mode := range []string{"foreign-schema", "altered", "extra-row", "empty-row", "symlink-sidecar", "cancel", "alias-parent"} {
		t.Run(mode, func(t *testing.T) {
			cfg := config(t, 1)
			s := openStore(t, cfg)
			mustPut(t, s, reading())
			path := s.path + ".publisher-owner.sqlite"
			if mode == "symlink-sidecar" {
				target := filepath.Join(t.TempDir(), "foreign")
				os.WriteFile(target, []byte("do not touch"), 0600)
				if e := os.Symlink(target, path); e != nil {
					t.Skip(e)
				}
			}
			if mode == "foreign-schema" || mode == "altered" || mode == "extra-row" || mode == "empty-row" {
				db, e := sql.Open("sqlite", path)
				if e != nil {
					t.Fatal(e)
				}
				query := ownerSQL + ";INSERT INTO admission_publisher_owner VALUES(1)"
				switch mode {
				case "foreign-schema":
					query = "CREATE TABLE foreign_data(value TEXT)"
				case "altered":
					query = "CREATE TABLE admission_publisher_owner(version INTEGER)"
				case "extra-row":
					query += ";INSERT INTO admission_publisher_owner VALUES(1)"
				case "empty-row":
					query = ownerSQL
				}
				if _, e = db.Exec(query); e != nil {
					t.Fatal(e)
				}
				db.Close()
			}
			c, _ := trust(t)
			calls := 0
			p := publisher(t, s, c, func(context.Context, Item) ([]byte, error) { calls++; return nil, errors.New("uncertain") }, 1)
			if mode == "alias-parent" {
				alias := filepath.Join(t.TempDir(), "alias")
				if e := os.Symlink(filepath.Dir(cfg.Path), alias); e != nil {
					t.Skip(e)
				}
				otherCfg := cfg
				otherCfg.Path = filepath.Join(alias, filepath.Base(cfg.Path))
				other := openStore(t, otherCfg)
				if other.path != s.path {
					t.Fatal("aliases diverged", other.path, s.path)
				}
				guard, e := other.acquireOwnership(t.Context())
				if e != nil {
					t.Fatal(e)
				}
				defer guard.release(true)
			}
			ctx := t.Context()
			if mode == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			result, e := p.RunOnce(ctx)
			if e == nil || result.Attempted != 0 || calls != 0 {
				t.Fatal(result, e, calls)
			}
			if mode != "cancel" && !errors.Is(e, ErrOwnership) {
				t.Fatal(e)
			}
			if mode == "symlink-sidecar" {
				b, e := os.ReadFile(path)
				if e != nil || strings.TrimSpace(string(b)) != "do not touch" {
					t.Fatal(string(b), e)
				}
			}
		})
	}
}
