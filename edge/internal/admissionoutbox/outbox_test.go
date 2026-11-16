package admissionoutbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func reading() Reading {
	return Reading{EventID: "event", TenantID: "tenant", GatewayID: "gateway", DeviceID: "device", PointID: "point", ObservedAt: time.Date(2026, 11, 16, 0, 0, 0, 123456000, time.UTC), Value: 42, Unit: "C", Quality: "measured", SchemaVersion: 1}
}
func config(t *testing.T, cap int) Config {
	t.Helper()
	return Config{Enabled: true, Path: filepath.Join(t.TempDir(), "admission-v1.sqlite"), Capacity: cap}
}
func openStore(t *testing.T, c Config) *Store {
	t.Helper()
	s, e := Open(t.Context(), c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func mustPut(t *testing.T, s *Store, r Reading) Item {
	t.Helper()
	it, e := s.Put(t.Context(), r)
	if e != nil {
		t.Fatal(e)
	}
	return it
}
func receipt(it Item) Receipt {
	r := it.Reading
	return Receipt{Version: 1, Policy: Policy, TenantID: r.TenantID, GatewayID: r.GatewayID, DeviceID: r.DeviceID, PointID: r.PointID, EventID: r.EventID, ObservedAt: r.ObservedAt, InputSHA256: it.InputSHA256, State: "pending", New: true}
}
func TestDisabledAndSeparateDatabase(t *testing.T) {
	c := config(t, 2)
	c.Enabled = false
	if s, e := Open(t.Context(), c); s != nil || !errors.Is(e, ErrDisabled) {
		t.Fatal(s, e)
	}
	if _, e := os.Stat(c.Path); !os.IsNotExist(e) {
		t.Fatal("disabled path touched", e)
	}
	c.Enabled = true
	db, e := sql.Open("sqlite", c.Path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`CREATE TABLE outbox(id INTEGER PRIMARY KEY,topic TEXT,payload BLOB);INSERT INTO outbox VALUES(1,'legacy','untouched')`); e != nil {
		t.Fatal(e)
	}
	db.Close()
	if s, e := Open(t.Context(), c); s != nil || !errors.Is(e, ErrSchema) {
		t.Fatal(s, e)
	}
	db, e = sql.Open("sqlite", c.Path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var topic, mode string
	var n int
	if e = db.QueryRow(`SELECT topic FROM outbox WHERE id=1`).Scan(&topic); e != nil || topic != "legacy" {
		t.Fatal(topic, e)
	}
	db.QueryRow(`PRAGMA journal_mode`).Scan(&mode)
	db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&n)
	if n != 1 || mode != "delete" {
		t.Fatal(n, mode)
	}
	for _, bad := range []Config{{Enabled: true, Path: c.Path, Capacity: 0}, {Enabled: true, Path: "relative", Capacity: 2}, {Enabled: true, Path: c.Path, Capacity: MaxCapacity + 1}} {
		if s, e := Open(t.Context(), bad); s != nil || e == nil {
			t.Fatal(s, e)
		}
	}
}
func TestCanonicalServerVectorAndRefusals(t *testing.T) {
	r := reading()
	b, d, e := canonical(r)
	if e != nil {
		t.Fatal(e)
	}
	// Matches the independently pinned server/internal/ingestprocessing vector.
	const vector = `{"event_id":"event","tenant_id":"tenant","gateway_id":"gateway","device_id":"device","point_id":"point","observed_at":"2026-11-16T00:00:00.123456Z","value":42,"unit":"C","quality":"measured","schema_version":1}`
	sum := sha256.Sum256([]byte(vector))
	if string(b) != vector || d != hex.EncodeToString(sum[:]) {
		t.Fatal(string(b), d)
	}
	for _, mode := range []string{"submicro", "offset", "zero", "nan", "inf", "quality", "schema", "unit", "invalidutf8", "empty", "whitespace", "wildcard"} {
		t.Run(mode, func(t *testing.T) {
			x := r
			switch mode {
			case "submicro":
				x.ObservedAt = x.ObservedAt.Add(time.Nanosecond)
			case "offset":
				x.ObservedAt = x.ObservedAt.In(time.FixedZone("offset", 3600))
			case "zero":
				x.ObservedAt = time.Time{}
			case "nan":
				x.Value = math.NaN()
			case "inf":
				x.Value = math.Inf(1)
			case "quality":
				x.Quality = "unknown"
			case "schema":
				x.SchemaVersion = 2
			case "unit":
				x.Unit = strings.Repeat("x", 65)
			case "invalidutf8":
				x.Unit = string([]byte{255})
			case "empty":
				x.EventID = ""
			case "whitespace":
				x.DeviceID = "d x"
			case "wildcard":
				x.GatewayID = "+"
			}
			if _, _, e := canonical(x); !errors.Is(e, ErrInput) {
				t.Fatal(e)
			}
		})
	}
}
func TestRetainDeduplicateCapacityAndReopen(t *testing.T) {
	c := config(t, 2)
	s := openStore(t, c)
	a := mustPut(t, s, reading())
	b := mustPut(t, s, reading())
	if !reflect.DeepEqual(a, b) {
		t.Fatal(a, b)
	}
	r := reading()
	r.Value++
	if it, e := s.Put(t.Context(), r); it.Sequence != 0 || !errors.Is(e, ErrConflict) {
		t.Fatal(it, e)
	}
	r = reading()
	r.EventID = "second"
	second := mustPut(t, s, r)
	r.EventID = "third"
	if it, e := s.Put(t.Context(), r); it.Sequence != 0 || !errors.Is(e, ErrCapacity) {
		t.Fatal(it, e)
	}
	// Exact duplicate is available even while full. No oldest-row eviction.
	if it, e := s.Put(t.Context(), reading()); e != nil || it.Sequence != a.Sequence {
		t.Fatal(it, e)
	}
	s.Close()
	s = openStore(t, c)
	got, e := s.Next(t.Context(), 2)
	if e != nil || len(got) != 2 || !reflect.DeepEqual(got[0], a) || !reflect.DeepEqual(got[1], second) {
		t.Fatal(got, e)
	}
	var mode string
	var sync int
	s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode)
	s.db.QueryRow(`PRAGMA synchronous`).Scan(&sync)
	if mode != "wal" || sync != 2 {
		t.Fatal(mode, sync)
	}
	changed := c
	changed.Capacity++
	if other, e := Open(t.Context(), changed); other != nil || !errors.Is(e, ErrSchema) {
		t.Fatal(other, e)
	}
}
func TestReceiptMatchingIsNonDestructive(t *testing.T) {
	s := openStore(t, config(t, 1))
	it := mustPut(t, s, reading())
	r := receipt(it)
	enc, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	var decoded Receipt
	if e = json.Unmarshal(enc, &decoded); e != nil || MatchReceipt(it, decoded) != nil {
		t.Fatal(decoded, e)
	}
	for _, field := range []string{"version", "policy", "tenant", "gateway", "device", "point", "event", "time", "offset", "digest", "digestcase", "state", "new"} {
		t.Run(field, func(t *testing.T) {
			x := r
			switch field {
			case "version":
				x.Version++
			case "policy":
				x.Policy = "other"
			case "tenant":
				x.TenantID += "x"
			case "gateway":
				x.GatewayID += "x"
			case "device":
				x.DeviceID += "x"
			case "point":
				x.PointID += "x"
			case "event":
				x.EventID += "x"
			case "time":
				x.ObservedAt = x.ObservedAt.Add(time.Microsecond)
			case "offset":
				x.ObservedAt = x.ObservedAt.In(time.FixedZone("offset", 3600))
			case "digest":
				x.InputSHA256 = strings.Repeat("0", 64)
			case "digestcase":
				x.InputSHA256 = strings.ToUpper(x.InputSHA256)
			case "state":
				x.State = "delivered"
			case "new":
				x.State = "completed"
			}
			if !errors.Is(MatchReceipt(it, x), ErrEvidence) {
				t.Fatal(x)
			}
		})
	}
	for _, state := range []string{"pending", "executing", "outcome_unknown", "completed"} {
		x := r
		x.State = state
		x.New = false
		if e := MatchReceipt(it, x); e != nil {
			t.Fatal(e)
		}
	}
	// Successful comparison is possible without any authenticated source. It must
	// NEVER imply deletion/delivery or mutate storage, even for completed state.
	got, e := s.Next(t.Context(), 1)
	if e != nil || len(got) != 1 || !reflect.DeepEqual(got[0], it) {
		t.Fatal(got, e)
	}
	bad := it
	bad.Payload = append([]byte(nil), it.Payload...)
	bad.Payload[0] = '['
	if MatchReceipt(bad, r) == nil {
		t.Fatal("tampered item matched")
	}
}
func TestCorruptionRefusesWholeBatch(t *testing.T) {
	for _, sqlText := range []string{`UPDATE admission_inputs SET sequence=0 WHERE sequence=2`, `UPDATE admission_inputs SET digest='bad' WHERE sequence=2`, `UPDATE admission_inputs SET topic='foreign' WHERE sequence=2`, `UPDATE admission_inputs SET event_id='foreign' WHERE sequence=2`, `UPDATE admission_inputs SET observed_at='foreign' WHERE sequence=2`, `UPDATE admission_inputs SET payload='{}' WHERE sequence=2`} {
		t.Run(sqlText, func(t *testing.T) {
			s := openStore(t, config(t, 3))
			mustPut(t, s, reading())
			r := reading()
			r.EventID = "second"
			mustPut(t, s, r)
			if _, e := s.db.Exec(sqlText); e != nil {
				t.Fatal(e)
			}
			if batch, e := s.Next(t.Context(), 2); batch != nil || !errors.Is(e, ErrEvidence) {
				t.Fatal(batch, e)
			}
			var n int
			s.db.QueryRow(`SELECT count(*) FROM admission_inputs`).Scan(&n)
			if n != 2 {
				t.Fatal(n)
			}
		})
	}
}
func TestConcurrentHandlesNeverOverfill(t *testing.T) {
	c := config(t, 1)
	a := openStore(t, c)
	b := openStore(t, c)
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for i, s := range []*Store{a, b} {
		wg.Add(1)
		go func(i int, s *Store) {
			defer wg.Done()
			r := reading()
			r.EventID += string(rune('a' + i))
			if _, e := s.Put(t.Context(), r); e == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}(i, s)
	}
	wg.Wait()
	got, e := a.Next(t.Context(), 2)
	if e != nil || len(got) != 1 || success != 1 {
		t.Fatal(got, e, success)
	}
}
func TestAbruptProcessRetainsCommittedInput(t *testing.T) {
	if p := os.Getenv("ADMISSION_OUTBOX_CHILD_PATH"); p != "" {
		s, e := Open(context.Background(), Config{Enabled: true, Path: p, Capacity: 2})
		if e != nil {
			os.Exit(31)
		}
		it, e := s.Put(context.Background(), reading())
		if e != nil || it.Sequence != 1 {
			os.Exit(32)
		}
		if e = os.WriteFile(p+".committed", []byte("committed"), 0600); e != nil {
			os.Exit(33)
		}
		select {}
	}
	c := config(t, 2)
	cmd := exec.Command(os.Args[0], "-test.run=^TestAbruptProcessRetainsCommittedInput$")
	cmd.Env = append(os.Environ(), "ADMISSION_OUTBOX_CHILD_PATH="+c.Path)
	var log bytes.Buffer
	cmd.Stdout = &log
	cmd.Stderr = &log
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, e := os.Stat(c.Path + ".committed"); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not commit", log.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e := cmd.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	cmd.Wait()
	s := openStore(t, c)
	got, e := s.Next(t.Context(), 2)
	if e != nil || len(got) != 1 || got[0].Sequence != 1 || got[0].Reading != reading() {
		t.Fatal(got, e)
	}
	if x := mustPut(t, s, reading()); x.Sequence != 1 {
		t.Fatal(x)
	}
	// Process SIGKILL is not disk power-loss, filesystem, hardware or owner proof.
}

func TestRefusedOperationsNeverReturnPositiveEvidence(t *testing.T) {
	var zero *Store
	if it, e := zero.Put(t.Context(), reading()); it.Sequence != 0 || !errors.Is(e, ErrDisabled) {
		t.Fatal(it, e)
	}
	if rows, e := zero.Next(t.Context(), 1); rows != nil || !errors.Is(e, ErrDisabled) {
		t.Fatal(rows, e)
	}
	s := openStore(t, config(t, 2))
	if it, e := s.Put(nil, reading()); it.Sequence != 0 || !errors.Is(e, ErrInput) {
		t.Fatal(it, e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if it, e := s.Put(ctx, reading()); it.Sequence != 0 || !errors.Is(e, context.Canceled) {
		t.Fatal(it, e)
	}
	if rows, e := s.Next(t.Context(), 257); rows != nil || !errors.Is(e, ErrInput) {
		t.Fatal(rows, e)
	}
	if rows, e := s.Next(t.Context(), 2); e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	s.Close()
	if it, e := s.Put(t.Context(), reading()); it.Sequence != 0 || e == nil {
		t.Fatal(it, e)
	}
}
func TestSchemaAndPolicyChangesAreNotMigrated(t *testing.T) {
	for _, change := range []string{`UPDATE admission_meta SET policy='other'`, `UPDATE admission_meta SET version=2`, `INSERT INTO admission_meta SELECT * FROM admission_meta`, `CREATE TRIGGER changed AFTER INSERT ON admission_inputs BEGIN DELETE FROM admission_inputs; END`, `CREATE TRIGGER sqliteXshadow AFTER INSERT ON admission_inputs BEGIN DELETE FROM admission_inputs; END`, `ALTER TABLE admission_inputs ADD COLUMN extra TEXT`} {
		t.Run(change, func(t *testing.T) {
			c := config(t, 2)
			s := openStore(t, c)
			it := mustPut(t, s, reading())
			if _, e := s.db.Exec(change); e != nil {
				t.Fatal(e)
			}
			s.Close()
			other, e := Open(t.Context(), c)
			if other != nil || !errors.Is(e, ErrSchema) {
				t.Fatal(other, e)
			}
			db, e := sql.Open("sqlite", c.Path)
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			var b []byte
			if e = db.QueryRow(`SELECT payload FROM admission_inputs WHERE sequence=1`).Scan(&b); e != nil || !bytes.Equal(b, it.Payload) {
				t.Fatal(string(b), e)
			}
		})
	}
}
