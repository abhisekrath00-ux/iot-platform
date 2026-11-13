package cmdexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func replayGate(t *testing.T, path string) (*Gate, *SQLiteReplay) {
	t.Helper()
	s, err := OpenReplay(path)
	if err != nil {
		t.Fatal(err)
	}
	g := NewGate([]string{"unlock"})
	g.Store = s
	return g, s
}
func TestPersistentReplayRestartAndFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "commands.sqlite")
	g, s := replayGate(t, path)
	a := &Simulated{}
	ack, _ := Handle(context.Background(), g, a, env(nil), t0)
	if ack.State != "acked" {
		t.Fatal(ack)
	}
	s.Close()
	g, s = replayGate(t, path)
	defer s.Close()
	ack, _ = Handle(context.Background(), g, a, env(nil), t0.Add(time.Second))
	if ack.State != "rejected" || ack.Detail != ErrReplay.Error() || len(a.Seen) != 1 {
		t.Fatalf("replayed after restart %+v count%d", ack, len(a.Seen))
	}
	// Closed store cannot fall back to the gate's memory or invoke an actuator.
	s.Close()
	ack, _ = Handle(context.Background(), g, a, env(func(e *Envelope) { e.RequestID = "new" }), t0)
	if ack.State != "rejected" || ack.Detail != ErrReplayStore.Error() || len(a.Seen) != 1 {
		t.Fatalf("store failure %+v", ack)
	}
	bad := filepath.Join(t.TempDir(), "bad.sqlite")
	if err := os.WriteFile(bad, []byte("not sqlite"), 0600); err != nil {
		t.Fatal(err)
	}
	if store, err := OpenReplay(bad); err == nil {
		store.Close()
		t.Fatal("corrupt store accepted")
	}
}
func TestPersistentReplayIndependentConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "commands.sqlite")
	var gs []*Gate
	for i := 0; i < 12; i++ {
		g, s := replayGate(t, path)
		defer s.Close()
		gs = append(gs, g)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for _, g := range gs {
		wg.Add(1)
		go func(g *Gate) {
			defer wg.Done()
			_, err := g.Check(env(nil), t0)
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, ErrReplay) {
				t.Errorf("claim: %v", err)
			}
		}(g)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("claims%d", wins.Load())
	}
}
func TestPersistentReplayExpiryRollbackAndCapacity(t *testing.T) {
	g, s := replayGate(t, filepath.Join(t.TempDir(), "commands.sqlite"))
	defer s.Close()
	if _, err := g.Check(env(nil), t0); err != nil {
		t.Fatal(err)
	}
	later := t0.Add(6 * time.Minute)
	if err := s.Claim("later", later.Add(time.Minute), later); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM command_replay WHERE request_id='r1'`).Scan(&n)
	if n != 0 {
		t.Fatal("expired claim not pruned")
	}
	// A backwards clock MUST NOT permit that pruned envelope to be actuated.
	if _, err := g.Check(env(nil), t0.Add(time.Second)); !errors.Is(err, ErrReplayStore) {
		t.Fatalf("clock rollback: %v", err)
	}
	if err := s.Claim("caught-up", later.Add(time.Minute), later); err != nil {
		t.Fatal(err)
	}
	_, err := s.db.Exec(`WITH RECURSIVE x(n) AS(VALUES(1) UNION ALL SELECT n+1 FROM x WHERE n<100000) INSERT INTO command_replay SELECT 'fill-'||n,? FROM x`, later.Add(time.Minute).UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Claim("full", later.Add(time.Minute), later); !errors.Is(err, ErrReplayStore) {
		t.Fatalf("capacity: %v", err)
	}
}
func TestPersistentReplayRejectsExtraJSON(t *testing.T) {
	g := NewGate([]string{"unlock"})
	if _, err := g.Check(append(env(nil), []byte(` {"evil":true}`)...), t0); !errors.Is(err, ErrMalformed) {
		t.Fatal(err)
	}
}

func TestPersistentReplayProcessExit(t *testing.T) {
	if path := os.Getenv("HEX_REPLAY_CRASH_PATH"); path != "" {
		g, s := replayGate(t, path)
		defer s.Close()
		if _, err := g.Check(env(nil), t0); err != nil {
			t.Fatal(err)
		}
		// Abrupt exit AFTER the durable claim, BEFORE any actuator or ACK.
		os.Exit(73)
	}
	path := filepath.Join(t.TempDir(), "commands.sqlite")
	child := exec.Command(os.Args[0], "-test.run=^TestPersistentReplayProcessExit$")
	child.Env = append(os.Environ(), "HEX_REPLAY_CRASH_PATH="+path)
	err := child.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 73 {
		t.Fatalf("child: %v", err)
	}
	g, s := replayGate(t, path)
	defer s.Close()
	a := &Simulated{}
	ack, _ := Handle(context.Background(), g, a, env(nil), t0)
	if ack.State != "rejected" || ack.Detail != ErrReplay.Error() || len(a.Seen) != 0 {
		t.Fatalf("uncertain claim rearmed %+v effects%d", ack, len(a.Seen))
	}
}

func TestPersistentReplayBusyStoreRefuses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "commands.sqlite")
	_, first := replayGate(t, path)
	defer first.Close()
	g, second := replayGate(t, path)
	defer second.Close()
	tx, err := first.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE command_clock SET high_ns=0`); err != nil {
		t.Fatal(err)
	}
	a := &Simulated{}
	start := time.Now()
	ack, _ := Handle(context.Background(), g, a, env(nil), t0)
	if ack.State != "rejected" || ack.Detail != ErrReplayStore.Error() || len(a.Seen) != 0 {
		t.Fatalf("busy store %+v effects%d", ack, len(a.Seen))
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("unbounded store wait")
	}
}

type slowClaim struct{}

func (slowClaim) Claim(string, time.Time, time.Time) error {
	time.Sleep(25 * time.Millisecond)
	return nil
}
func TestPersistentAdmissionDelayCannotStartExpiredEffect(t *testing.T) {
	g := NewGate([]string{"unlock"})
	g.Store = slowClaim{}
	a := &Simulated{}
	ack, _ := Handle(context.Background(), g, a, env(func(e *Envelope) { e.ExpiresAt = t0.Add(10 * time.Millisecond) }), t0)
	if ack.State != "rejected" || ack.Detail != ErrExpired.Error() || len(a.Seen) != 0 {
		t.Fatal(ack)
	}
}

func TestPersistentStoreSettingsAndCanceledEffect(t *testing.T) {
	g, s := replayGate(t, filepath.Join(t.TempDir(), "commands.sqlite"))
	defer s.Close()
	var sync int
	var mode string
	if err := s.db.QueryRow(`PRAGMA synchronous`).Scan(&sync); err != nil || sync != 2 {
		t.Fatalf("synchronous=%d err%v", sync, err)
	}
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal=%s err%v", mode, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := &Simulated{}
	ack, _ := Handle(ctx, g, a, env(nil), t0)
	if ack.State != "rejected" || len(a.Seen) != 0 {
		t.Fatalf("canceled effect %+v", ack)
	}
}

type exitAfterMarker struct{ path string }

func (a exitAfterMarker) Execute(context.Context, *Envelope) (string, error) {
	f, err := os.OpenFile(a.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	if _, err = f.WriteString("simulated effect\n"); err != nil {
		f.Close()
		return "", err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return "", err
	}
	f.Close()
	os.Exit(74)
	return "", nil
}
func TestPersistentReplayExitAfterSimulatedEffect(t *testing.T) {
	if path := os.Getenv("HEX_REPLAY_EFFECT_PATH"); path != "" {
		g, s := replayGate(t, path)
		defer s.Close()
		Handle(context.Background(), g, exitAfterMarker{path + ".marker"}, env(nil), t0)
		t.Fatal("child did not exit")
	}
	path := filepath.Join(t.TempDir(), "commands.sqlite")
	child := exec.Command(os.Args[0], "-test.run=^TestPersistentReplayExitAfterSimulatedEffect$")
	child.Env = append(os.Environ(), "HEX_REPLAY_EFFECT_PATH="+path)
	err := child.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 74 {
		t.Fatalf("child: %v", err)
	}
	g, s := replayGate(t, path)
	defer s.Close()
	a := &Simulated{}
	ack, _ := Handle(context.Background(), g, a, env(nil), t0)
	if ack.State != "rejected" || ack.Detail != ErrReplay.Error() || len(a.Seen) != 0 {
		t.Fatalf("replayed after effect %+v", ack)
	}
	marker, err := os.ReadFile(path + ".marker")
	if err != nil || string(marker) != "simulated effect\n" {
		t.Fatalf("marker %q err%v", marker, err)
	}
}
func TestPersistentReplaySymlinkRefused(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, nil, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlink unavailable")
	}
	if s, err := OpenReplay(link); err == nil {
		s.Close()
		t.Fatal("symlink accepted")
	}
}
