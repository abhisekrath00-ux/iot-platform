package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/indexsink"
)

// fakeBulk is a stand-in for OpenSearch's _bulk endpoint. It only proves the sink speaks the documented
// NDJSON bulk format; it is not a real OpenSearch.
type fakeBulk struct {
	mu     sync.Mutex
	docs   map[string]map[string]any // _id -> source
	calls  int
	status int  // forced HTTP status when non-zero
	reject bool // answer 200 with a failed item
	auth   string
	dups   int // documents delivered again after already being stored
}

func (f *fakeBulk) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.auth = r.Header.Get("Authorization")
	if r.URL.Path != "/_bulk" || r.Method != "POST" || r.Header.Get("Content-Type") != "application/x-ndjson" {
		w.WriteHeader(400)
		return
	}
	if f.status != 0 {
		w.WriteHeader(f.status)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	items := []any{}
	for i := 0; i+1 < len(lines); i += 2 {
		var meta map[string]map[string]string
		var src map[string]any
		json.Unmarshal([]byte(lines[i]), &meta)
		json.Unmarshal([]byte(lines[i+1]), &src)
		st := 201
		if f.reject {
			st = 400
		} else {
			if _, seen := f.docs[meta["index"]["_id"]]; seen {
				f.dups++
			}
			f.docs[meta["index"]["_id"]] = src
		}
		items = append(items, map[string]any{"index": map[string]any{"status": st}})
	}
	json.NewEncoder(w).Encode(map[string]any{"errors": f.reject, "items": items})
}

func TestIntegrationIndexSink(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-isk")
	ctx := t.Context()
	pool := s.st.Pool
	const sink = "itest-sink"
	clean := func() {
		pool.Exec(ctx, `DELETE FROM telemetry WHERE tenant_id='itest-isk'`)
		pool.Exec(ctx, `DELETE FROM index_sink_cursor WHERE name=$1`, sink)
	}
	clean()
	t.Cleanup(clean)
	f := &fakeBulk{docs: map[string]map[string]any{}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	cfg := &indexsink.Config{URL: srv.URL, Index: "hexmon-telemetry", Batch: 1000, Lag: 5 * time.Second, Name: sink, User: "u", Password: "p"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	// start just before our rows so other tests' data in a shared database is not the subject
	start := time.Now().Add(-10 * time.Minute).Truncate(time.Microsecond)
	pool.Exec(ctx, `INSERT INTO index_sink_cursor(name,received_at,event_id) VALUES($1,$2,'')`, sink, start)
	ins := func(id string, ago time.Duration, v float64) {
		if _, err := pool.Exec(ctx, `INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,received_at,value,unit,quality,schema_version)
			VALUES($1,'itest-isk','itest-isk-gw','d1','p1',$2,$2,$3,'C','measured',1)`, id, time.Now().Add(-ago), v); err != nil {
			t.Fatal(err)
		}
	}
	ins("isk-1", 60*time.Second, 1.5)
	ins("isk-2", 50*time.Second, 2.5)
	ins("isk-fresh", 1*time.Second, 9) // inside the lag window: must wait
	k := indexsink.New(cfg, pool)

	// a server error leaves the cursor where it was and records the error
	f.status = 503
	if _, err := k.Step(ctx); err == nil {
		t.Fatal("503 was not reported")
	}
	var errMsg string
	pool.QueryRow(ctx, `SELECT last_error FROM index_sink_cursor WHERE name=$1`, sink).Scan(&errMsg)
	if errMsg == "" || len(f.docs) != 0 {
		t.Fatalf("error not recorded or docs stored: %q %d", errMsg, len(f.docs))
	}
	// a 200 that rejects an item must not advance either
	f.status, f.reject = 0, true
	if _, err := k.Step(ctx); err == nil {
		t.Fatal("rejected item was treated as success")
	}
	var cur time.Time
	pool.QueryRow(ctx, `SELECT received_at FROM index_sink_cursor WHERE name=$1`, sink).Scan(&cur)
	if !cur.Equal(start) {
		t.Fatalf("cursor moved on failure: %v", cur)
	}
	// success: both old rows arrive with their tenant and values, the fresh one does not, auth was sent
	f.reject = false
	if _, err := k.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if d := f.docs["isk-1"]; d == nil || d["tenant_id"] != "itest-isk" || d["value"] != 1.5 || d["point_id"] != "p1" || d["@timestamp"] == nil {
		t.Fatalf("doc isk-1 = %v", d)
	}
	if f.docs["isk-2"] == nil {
		t.Fatal("isk-2 missing")
	}
	if f.docs["isk-fresh"] != nil {
		t.Fatal("a row inside the lag window was pushed")
	}
	if !strings.HasPrefix(f.auth, "Basic ") {
		t.Fatalf("auth header %q", f.auth)
	}
	// nothing new: a second step sends nothing (no duplicates), then a later row is picked up
	// Other tests write telemetry into the shared database, and those rows can age past the lag window while this
	// test runs, so first drain whatever is eligible, then check that an immediate second step sends nothing.
	for i := 0; i < 50; i++ {
		if n, err := k.Step(ctx); err != nil || n == 0 {
			break
		}
	}
	// Rows from other tests (or isk-fresh itself) can still age into the window at any moment, so an idle step may
	// legitimately send NEW rows. What it must never do is send a document it already delivered.
	dupsBefore := f.dups
	if _, err := k.Step(ctx); err != nil || f.dups != dupsBefore {
		t.Fatalf("idle step err=%v re-sent %d already delivered documents", err, f.dups-dupsBefore)
	}
	// the fresh row has aged past the lag window. Rewind the cursor (other tests write to a shared database, so
	// the cursor may already be past it); replaying earlier rows is harmless because ids are idempotent.
	pool.Exec(ctx, `UPDATE telemetry SET received_at=now()-interval '20 seconds' WHERE event_id='isk-fresh'`)
	pool.Exec(ctx, `UPDATE index_sink_cursor SET received_at=$2, event_id='' WHERE name=$1`, sink, start)
	if _, err := k.Step(ctx); err != nil || f.docs["isk-fresh"] == nil {
		t.Fatalf("late row not pushed: %v", err)
	}
	var pushed int
	var lastErr string
	pool.QueryRow(ctx, `SELECT pushed,last_error FROM index_sink_cursor WHERE name=$1`, sink).Scan(&pushed, &lastErr)
	if pushed < 3 || lastErr != "" {
		t.Fatalf("cursor stats pushed=%d err=%q", pushed, lastErr)
	}
}

func TestIndexSinkConfig(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if c, err := indexsink.FromEnv(env(nil)); c != nil || err != nil {
		t.Fatal("must be off by default")
	}
	for name, m := range map[string]map[string]string{
		"plain http to a remote host": {"INDEX_SINK_URL": "http://search.example.com:9200"},
		"not a url":                   {"INDEX_SINK_URL": "ftp://x"},
		"bad index":                   {"INDEX_SINK_URL": "https://x.example", "INDEX_SINK_INDEX": "A/B"},
		"underscore index":            {"INDEX_SINK_URL": "https://x.example", "INDEX_SINK_INDEX": "_all"},
	} {
		if c, err := indexsink.FromEnv(env(m)); err == nil {
			t.Errorf("%s accepted: %+v", name, c)
		}
	}
	for name, m := range map[string]map[string]string{
		"https remote":     {"INDEX_SINK_URL": "https://search.example.com:9200"},
		"http loopback":    {"INDEX_SINK_URL": "http://127.0.0.1:9200"},
		"http with opt-in": {"INDEX_SINK_URL": "http://10.0.0.5:9200", "INDEX_SINK_ALLOW_INSECURE": "1"},
	} {
		if _, err := indexsink.FromEnv(env(m)); err != nil {
			t.Errorf("%s refused: %v", name, err)
		}
	}
}
