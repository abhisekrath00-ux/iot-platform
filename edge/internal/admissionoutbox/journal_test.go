package admissionoutbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/driver"
)

func journalConfig(t *testing.T) JournalConfig {
	return JournalConfig{Enabled: true, Path: filepath.Join(t.TempDir(), "pending.sqlite")}
}
func journalCollector(t *testing.T, cfg CollectorConfig, jcfg JournalConfig) *Collector {
	t.Helper()
	c, e := NewJournalCollector(t.Context(), cfg, jcfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Close(context.Background()) })
	return c
}
func journalSession(t *testing.T) *Session {
	cfg := sessionConfig(t)
	cfg.Store.Capacity = 2
	return sessionOpen(t, cfg)
}
func journalReader(polls *int) PollReader {
	return pollFunc(func(context.Context) ([]driver.Reading, error) { *polls++; return sourceSamples(), nil })
}
func fillOne(t *testing.T, s *Session) Item {
	r := reading()
	r.TenantID = s.tenant
	r.GatewayID = s.gateway
	return mustPut(t, s.store, r)
}
func TestJournalOptInAndSeparateSchema(t *testing.T) {
	s := journalSession(t)
	polls := 0
	cfg := collectorConfig(s, journalReader(&polls))
	for _, mode := range []string{"disabled", "relative", "same-store", "store-guard", "invalid-collector", "nil-context", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			jc := journalConfig(t)
			cc := cfg
			ctx := t.Context()
			switch mode {
			case "disabled":
				jc.Enabled = false
			case "relative":
				jc.Path = "relative.sqlite"
			case "same-store":
				jc.Path = s.store.path
			case "store-guard":
				jc.Path = s.store.path + ".publisher-owner.sqlite"
			case "invalid-collector":
				cc.Points = nil
			case "nil-context":
				ctx = nil
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if c, e := NewJournalCollector(ctx, cc, jc); e == nil || c != nil {
				t.Fatal(c, e)
			}
			if polls != 0 {
				t.Fatal("read during constructor")
			}
			if mode != "same-store" {
				if _, e := os.Stat(jc.Path); !os.IsNotExist(e) {
					t.Fatal("refused path touched", e)
				}
			}
		})
	}
	jc := journalConfig(t)
	db, e := sql.Open("sqlite", jc.Path)
	if e != nil {
		t.Fatal(e)
	}
	db.Exec("CREATE TABLE legacy(x)")
	db.Close()
	if c, e := NewJournalCollector(t.Context(), cfg, jc); c != nil || !errors.Is(e, ErrSchema) {
		t.Fatal(c, e)
	}
	// A refused schema does not leak local ownership.
	if c, e := NewJournalCollector(t.Context(), cfg, jc); c != nil || !errors.Is(e, ErrSchema) {
		t.Fatal(c, e)
	}
}
func TestJournalCapacityReopenExactPendingNoAutoWork(t *testing.T) {
	s := journalSession(t)
	first := fillOne(t, s)
	polls := 0
	cfg := collectorConfig(s, journalReader(&polls))
	jc := journalConfig(t)
	c := journalCollector(t, cfg, jc)
	if r, e := c.PollOnce(t.Context()); !errors.Is(e, ErrCapacity) || r.Retained != 0 || r.Pending != 2 {
		t.Fatal(r, e)
	}
	frozen := append([]Reading(nil), c.pending...)
	if c.Status().PendingDurability != "journal-committed" {
		t.Fatal(c.Status())
	}
	if _, e := NewJournalCollector(t.Context(), cfg, jc); !errors.Is(e, ErrOwnership) {
		t.Fatal("second local owner", e)
	}
	if e := c.Stop(t.Context()); e != nil {
		t.Fatal(e)
	}
	if _, e := NewJournalCollector(t.Context(), cfg, jc); !errors.Is(e, ErrOwnership) {
		t.Fatal("Stop released ownership", e)
	}
	if e := c.Close(t.Context()); e != nil {
		t.Fatal(e)
	}
	if _, e := c.RetainPending(t.Context()); !errors.Is(e, ErrSessionClosed) {
		t.Fatal(e)
	}
	c = journalCollector(t, cfg, jc)
	if !reflect.DeepEqual(c.pending, frozen) || c.Status().Outcome != "journal-recovered" || polls != 1 {
		t.Fatal(c.pending, c.Status(), polls)
	}
	if _, e := c.PollOnce(t.Context()); !errors.Is(e, ErrCollectorPending) || polls != 1 {
		t.Fatal(e, polls)
	}
	// Remove only the unrelated capacity occupant. No network or new source read.
	if _, e := s.store.db.Exec("DELETE FROM admission_inputs WHERE sequence=?", first.Sequence); e != nil {
		t.Fatal(e)
	}
	r, e := c.RetainPending(t.Context())
	if e != nil || r.Retained != 2 || r.Pending != 0 || polls != 1 {
		t.Fatal(r, e, polls)
	}
	rows, e := s.store.Next(t.Context(), 2)
	if e != nil || len(rows) != 2 || rows[0].Reading != frozen[0] || rows[1].Reading != frozen[1] {
		t.Fatal(rows, e)
	}
	c.Close(t.Context())
	c = journalCollector(t, cfg, jc)
	if c.Status().Pending != 0 || c.Status().PendingDurability != "none" || polls != 1 {
		t.Fatal(c.Status(), polls)
	}
}
func TestJournalScopeCorruptionAndNoOverwrite(t *testing.T) {
	for _, mode := range []string{"scope", "digest", "payload", "schema"} {
		t.Run(mode, func(t *testing.T) {
			s := journalSession(t)
			fillOne(t, s)
			polls := 0
			cfg := collectorConfig(s, journalReader(&polls))
			jc := journalConfig(t)
			c := journalCollector(t, cfg, jc)
			if _, e := c.PollOnce(t.Context()); !errors.Is(e, ErrCapacity) {
				t.Fatal(e)
			}
			original, e := c.journalBytes(c.pending)
			if e != nil {
				t.Fatal(e)
			}
			changed := append([]Reading(nil), c.pending...)
			changed[0].Value++
			b, e := c.journalBytes(changed)
			if e != nil {
				t.Fatal(e)
			}
			if e = c.journal.save(t.Context(), b); !errors.Is(e, ErrConflict) {
				t.Fatal(e)
			}
			if e = c.journal.clear(t.Context(), b); !errors.Is(e, ErrConflict) {
				t.Fatal(e)
			}
			var preserved []byte
			if e = c.journal.db.QueryRow("SELECT payload FROM collection_pending").Scan(&preserved); e != nil || !reflect.DeepEqual(preserved, original) {
				t.Fatal(e)
			}
			c.Close(t.Context())
			if mode == "scope" {
				cfg.Points = map[string]string{"p": "V", "other": "A"}
			} else {
				db, e := sql.Open("sqlite", jc.Path)
				if e != nil {
					t.Fatal(e)
				}
				switch mode {
				case "digest":
					_, e = db.Exec("UPDATE collection_pending SET digest='wrong'")
				case "payload":
					b = []byte(`[{"invalid":true}]`)
					_, e = db.Exec("UPDATE collection_pending SET payload=?,digest=?", b, digestBytes(b))
				case "schema":
					_, e = db.Exec("CREATE TABLE unexpected(x)")
				}
				if e != nil {
					t.Fatal(e)
				}
				db.Close()

			}
			if reopened, e := NewJournalCollector(t.Context(), cfg, jc); e == nil || reopened != nil || polls != 1 {
				t.Fatal(reopened, e, polls)
			}
		})
	}
}
func TestJournalClearFailureFreezesSameWholeInput(t *testing.T) {
	s := journalSession(t)
	polls := 0
	c := journalCollector(t, collectorConfig(s, journalReader(&polls)), journalConfig(t))
	if _, e := c.journal.db.Exec(`CREATE TRIGGER clear_fault BEFORE DELETE ON collection_pending BEGIN SELECT RAISE(ABORT,'synthetic clear refusal'); END`); e != nil {
		t.Fatal(e)
	}
	result, e := c.PollOnce(t.Context())
	if e == nil || result.Pending != 2 || c.Status().Outcome != "journal-clear-unresolved" {
		t.Fatal(result, e, c.Status())
	}
	frozen := append([]Reading(nil), c.pending...)
	rows, e := s.store.Next(t.Context(), 2)
	if e != nil || len(rows) != 2 {
		t.Fatal(rows, e)
	}
	if _, e = c.PollOnce(t.Context()); !errors.Is(e, ErrCollectorPending) || polls != 1 {
		t.Fatal(e, polls)
	}
	c.journal.db.Exec("DROP TRIGGER clear_fault")
	if result, e = c.RetainPending(t.Context()); e != nil || result.Pending != 0 || polls != 1 {
		t.Fatal(result, e, polls)
	}
	after, e := s.store.Next(t.Context(), 2)
	if e != nil || !reflect.DeepEqual(rows, after) || after[0].Reading != frozen[0] {
		t.Fatal(after, e)
	}
}
func TestJournalSaveFailureDoesNotRetainOrClaimDurability(t *testing.T) {
	s := journalSession(t)
	polls := 0
	c := journalCollector(t, collectorConfig(s, journalReader(&polls)), journalConfig(t))
	c.journal.db.Exec(`CREATE TRIGGER save_fault BEFORE INSERT ON collection_pending BEGIN SELECT RAISE(ABORT,'synthetic save refusal'); END`)
	r, e := c.PollOnce(t.Context())
	if e == nil || r.Pending != 2 || c.Status().PendingDurability != "journal-unconfirmed" {
		t.Fatal(r, e, c.Status())
	}
	rows, e := s.store.Next(t.Context(), 2)
	if e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	original := append([]Reading(nil), c.pending...)
	c.journal.db.Exec("DROP TRIGGER save_fault")
	if r, e = c.RetainPending(t.Context()); e != nil || r.Pending != 0 || polls != 1 {
		t.Fatal(r, e, polls)
	}
	rows, e = s.store.Next(t.Context(), 2)
	if e != nil || rows[0].Reading != original[0] || rows[1].Reading != original[1] {
		t.Fatal(rows, e)
	}
}
func TestJournalAbruptProcessExitRecovery(t *testing.T) {
	if root := os.Getenv("TEST_COLLECTION_JOURNAL_CHILD"); root != "" {
		cfg := sessionConfig(t)
		cfg.Store.Path = filepath.Join(root, "retained.sqlite")
		cfg.Store.Capacity = 2
		s := sessionOpen(t, cfg)
		fillOne(t, s)
		polls := 0
		c := journalCollector(t, collectorConfig(s, journalReader(&polls)), JournalConfig{Enabled: true, Path: filepath.Join(root, "journal.sqlite")})
		if _, e := c.PollOnce(t.Context()); !errors.Is(e, ErrCapacity) {
			panic(e)
		}
		b, e := json.Marshal(c.pending)
		if e != nil {
			panic(e)
		}
		if e = os.WriteFile(filepath.Join(root, "expected.json"), b, 0600); e != nil {
			panic(e)
		}
		// Exit WITHOUT Close: SQLite OS handles release ownership. Journal commit
		// already confirmed; this is process-exit evidence, not host power loss.
		os.Exit(0)
	}
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestJournalAbruptProcessExitRecovery$")
	cmd.Env = append(os.Environ(), "TEST_COLLECTION_JOURNAL_CHILD="+root)
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("child %v %s", e, b)
	}
	cfg := sessionConfig(t)
	cfg.Store.Path = filepath.Join(root, "retained.sqlite")
	cfg.Store.Capacity = 2
	s := sessionOpen(t, cfg)
	polls := 0
	c := journalCollector(t, collectorConfig(s, journalReader(&polls)), JournalConfig{Enabled: true, Path: filepath.Join(root, "journal.sqlite")})
	var want []Reading
	b, e := os.ReadFile(filepath.Join(root, "expected.json"))
	if e != nil || json.Unmarshal(b, &want) != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(c.pending, want) || polls != 0 || c.Status().PendingDurability != "journal-committed" {
		t.Fatal(c.pending, want, c.Status())
	}
	if _, e = c.PollOnce(t.Context()); !errors.Is(e, ErrCollectorPending) || polls != 0 {
		t.Fatal(e, polls)
	}
	s.store.db.Exec("DELETE FROM admission_inputs")
	if r, e := c.RetainPending(t.Context()); e != nil || r.Retained != 2 || r.Pending != 0 || polls != 0 {
		t.Fatal(r, e, polls)
	}
	rows, e := s.store.Next(t.Context(), 2)
	if e != nil || len(rows) != 2 || rows[0].Reading != want[0] || rows[1].Reading != want[1] {
		t.Fatal(rows, e)
	}
	t.Log("ACTUAL_COLLECTOR_JOURNAL_PROCESS_EXIT_EXACT_RECOVERY_OK")
}
func TestJournalCloseTimeoutKeepsOwnershipAndPreventsAllNewCalls(t *testing.T) {
	s := journalSession(t)
	entered := make(chan struct{})
	finish := make(chan struct{})
	done := make(chan error, 1)
	cfg := collectorConfig(s, pollFunc(func(context.Context) ([]driver.Reading, error) { close(entered); <-finish; return sourceSamples(), nil }))
	jc := journalConfig(t)
	c := journalCollector(t, cfg, jc)
	go func() { _, e := c.PollOnce(t.Context()); done <- e }()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if e := c.Close(ctx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	if _, e := c.RetainPending(t.Context()); !errors.Is(e, ErrSessionClosed) {
		t.Fatal(e)
	}
	if _, e := NewJournalCollector(t.Context(), cfg, jc); !errors.Is(e, ErrOwnership) {
		t.Fatal(e)
	}
	close(finish)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if e := c.Close(t.Context()); e != nil {
		t.Fatal(e)
	}
	reopened := journalCollector(t, cfg, jc)
	if reopened.Status().Pending != 0 {
		t.Fatal(reopened.Status())
	}
}
func TestJournalReopenAfterCommittedRetainAndUncertainClear(t *testing.T) {
	s := journalSession(t)
	polls := 0
	cfg := collectorConfig(s, journalReader(&polls))
	jc := journalConfig(t)
	c := journalCollector(t, cfg, jc)
	c.journal.db.Exec(`CREATE TRIGGER clear_fault BEFORE DELETE ON collection_pending BEGIN SELECT RAISE(ABORT,'synthetic clear refusal'); END`)
	if _, e := c.PollOnce(t.Context()); e == nil {
		t.Fatal("missing clear refusal")
	}
	frozen := append([]Reading(nil), c.pending...)
	rows, e := s.store.Next(t.Context(), 2)
	if e != nil || len(rows) != 2 {
		t.Fatal(rows, e)
	}
	c.journal.db.Exec("DROP TRIGGER clear_fault")
	c.Close(t.Context())
	c = journalCollector(t, cfg, jc)
	if !reflect.DeepEqual(c.pending, frozen) || polls != 1 {
		t.Fatal(c.pending, polls)
	}
	if r, e := c.RetainPending(t.Context()); e != nil || r.Pending != 0 || r.Retained != 2 || polls != 1 {
		t.Fatal(r, e, polls)
	}
	after, e := s.store.Next(t.Context(), 2)
	if e != nil || !reflect.DeepEqual(rows, after) {
		t.Fatal(after, e)
	}
}
