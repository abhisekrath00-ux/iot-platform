package admissionoutbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func batchReadings() []Reading {
	a := reading()
	b := a
	b.EventID = "second"
	b.PointID = "second"
	return []Reading{a, b}
}
func TestBatchWholeSampleAndSchemaReopen(t *testing.T) {
	cfg := config(t, 2)
	s := openStore(t, cfg)
	input := batchReadings()
	got, e := s.PutBatch(t.Context(), input)
	if e != nil || len(got) != 2 {
		t.Fatal(got, e)
	}
	again, e := s.PutBatch(t.Context(), input)
	if e != nil || !reflect.DeepEqual(got, again) {
		t.Fatal(got, again, e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s = openStore(t, cfg)
	// Existing single-row API and unchanged schema can still read exact batches.
	rows, e := s.Next(t.Context(), 2)
	if e != nil || !reflect.DeepEqual(rows, got) {
		t.Fatal(rows, got, e)
	}
	for i := range input {
		it, e := s.Put(t.Context(), input[i])
		if e != nil || !reflect.DeepEqual(it, got[i]) {
			t.Fatal(it, e)
		}
	}
}
func TestBatchRefusalLeavesNoPrefix(t *testing.T) {
	for _, mode := range []string{"empty", "too-many", "nil-context", "cancel", "invalid-last", "duplicate", "conflict-last", "capacity", "late-insert-fault"} {
		t.Run(mode, func(t *testing.T) {
			cfg := config(t, 2)
			s := openStore(t, cfg)
			input := batchReadings()
			ctx := t.Context()
			want := 0
			switch mode {
			case "empty":
				input = nil
			case "too-many":
				input = make([]Reading, 257)
			case "nil-context":
				ctx = nil
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "invalid-last":
				input[1].Unit = string(make([]byte, 65))
			case "duplicate":
				input[1] = input[0]
			case "conflict-last":
				mustPut(t, s, input[1])
				input[1].Value++
				want = 1
			case "capacity":
				r := reading()
				r.EventID = "existing"
				mustPut(t, s, r)
				want = 1
			case "late-insert-fault":
				_, e := s.db.Exec(`CREATE TRIGGER fail_second BEFORE INSERT ON admission_inputs WHEN NEW.event_id='second' BEGIN SELECT RAISE(ABORT,'synthetic second insert refusal'); END`)
				if e != nil {
					t.Fatal(e)
				}
			}
			if got, e := s.PutBatch(ctx, input); got != nil || e == nil {
				t.Fatal(got, e)
			}
			if mode == "late-insert-fault" {
				if _, e := s.db.Exec("DROP TRIGGER fail_second"); e != nil {
					t.Fatal(e)
				}
			}
			rows, e := s.Next(t.Context(), 2)
			if e != nil || len(rows) != want {
				t.Fatal(rows, e, want)
			}
			if e = s.Close(); e != nil {
				t.Fatal(e)
			}
			s = openStore(t, cfg)
			rows, e = s.Next(t.Context(), 2)
			if e != nil || len(rows) != want {
				t.Fatal(rows, e, want)
			}
		})
	}
}
func TestBatchMixedExistingAndNewAtCapacity(t *testing.T) {
	s := openStore(t, config(t, 2))
	input := batchReadings()
	first := mustPut(t, s, input[0])
	got, e := s.PutBatch(t.Context(), input)
	if e != nil || len(got) != 2 || got[0].Sequence != first.Sequence {
		t.Fatal(got, e)
	}
	got, e = s.PutBatch(t.Context(), input)
	if e != nil || len(got) != 2 {
		t.Fatal(got, e)
	}
	input[1].Value++
	if got, e = s.PutBatch(t.Context(), input); !errors.Is(e, ErrConflict) || got != nil {
		t.Fatal(got, e)
	}
}
func TestBatchSessionRejectsForeignWholeRequest(t *testing.T) {
	s := sessionOpen(t, sessionConfig(t))
	input := batchReadings()
	input[0].TenantID = s.tenant
	input[0].GatewayID = s.gateway
	input[1].TenantID = "foreign"
	if got, e := s.RetainBatch(t.Context(), input); got != nil || !errors.Is(e, ErrSignature) {
		t.Fatal(got, e)
	}
	status, e := s.Status(t.Context())
	if e != nil || status.Capacity.Retained != 0 || status.LastSend != nil {
		t.Fatal(status, e)
	}
	if e = s.Close(t.Context()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.RetainBatch(t.Context(), input); !errors.Is(e, ErrSessionClosed) {
		t.Fatal(e)
	}
}
func TestBatchCompetingLocalConnections(t *testing.T) {
	cfg := config(t, 2)
	a := openStore(t, cfg)
	b := openStore(t, cfg)
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i, s := range []*Store{a, b} {
		wg.Add(1)
		go func(i int, s *Store) {
			defer wg.Done()
			<-start
			in := batchReadings()
			if i == 1 {
				in[0].EventID = "other1"
				in[1].EventID = "other2"
			}
			_, e := s.PutBatch(t.Context(), in)
			results <- e
		}(i, s)
	}
	close(start)
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal(success)
	}
	rows, e := a.Next(t.Context(), 2)
	if e != nil || len(rows) != 2 {
		t.Fatal(rows, e)
	}
	if rows[0].Reading.EventID == "event" && rows[1].Reading.EventID != "second" || rows[0].Reading.EventID == "other1" && rows[1].Reading.EventID != "other2" {
		t.Fatal(rows)
	}
}
func TestBatchAbruptExitReopen(t *testing.T) {
	if path := os.Getenv("TEST_BATCH_CRASH_PATH"); path != "" {
		s, e := Open(context.Background(), Config{Enabled: true, Path: path, Capacity: 2})
		if e != nil {
			panic(e)
		}
		if os.Getenv("TEST_BATCH_CRASH_PHASE") == "committed" {
			if _, e = s.PutBatch(context.Background(), batchReadings()); e != nil {
				panic(e)
			}
			os.Exit(0)
		}
		// Deliberately stage only a prefix inside an uncommitted transaction. Abrupt
		// death must not make it visible. This exercises SQLite process recovery,
		// not host power loss or a real crash of the meter/source.
		tx, e := s.db.Begin()
		if e != nil {
			panic(e)
		}
		r := batchReadings()[0]
		payload, digest, e := canonical(r)
		if e != nil {
			panic(e)
		}
		if _, e = tx.Exec(`INSERT INTO admission_inputs(event_id,observed_at,topic,payload,digest) VALUES(?,?,?,?,?)`, r.EventID, r.ObservedAt.Format(time.RFC3339Nano), "t/tenant/g/gateway/telemetry", payload, digest); e != nil {
			panic(e)
		}
		os.Exit(0)
	}
	for _, phase := range []string{"uncommitted", "committed"} {
		t.Run(phase, func(t *testing.T) {
			cfg := Config{Enabled: true, Path: filepath.Join(t.TempDir(), "crash.sqlite"), Capacity: 2}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBatchAbruptExitReopen$")
			cmd.Env = append(os.Environ(), "TEST_BATCH_CRASH_PATH="+cfg.Path, "TEST_BATCH_CRASH_PHASE="+phase)
			if out, e := cmd.CombinedOutput(); e != nil {
				t.Fatalf("child: %v %s", e, out)
			}
			s := openStore(t, cfg)
			rows, e := s.Next(t.Context(), 2)
			want := 0
			if phase == "committed" {
				want = 2
			}
			if e != nil || len(rows) != want {
				t.Fatal(rows, e, want)
			}
			got, e := s.PutBatch(t.Context(), batchReadings())
			if e != nil || len(got) != 2 {
				t.Fatal(got, e)
			}
			if want == 2 && !reflect.DeepEqual(rows, got) {
				t.Fatal(rows, got)
			}
		})
	}
	t.Log("ATOMIC_BATCH_PROCESS_EXIT_REOPEN_OK")
}
