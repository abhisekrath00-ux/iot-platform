package main

import (
	"context"
	"errors"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/llm"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestIntegrationActionOutcomes(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-outcomes")
	ctx := context.Background()
	_, err := s.st.Pool.Exec(ctx, `DELETE FROM assistant_actions WHERE tenant_id='itest-outcomes'`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.st.Pool.Exec(ctx, `DELETE FROM assistant_actions WHERE tenant_id='itest-outcomes'`) })
	add := func(id, status string) {
		t.Helper()
		_, e := s.st.Pool.Exec(ctx, `INSERT INTO assistant_actions(id,tenant_id,user_id,method,path,status) VALUES($1,'itest-outcomes','test-user','POST','/v1/alerts/a/ack',$2)`, id, status)
		if e != nil {
			t.Fatal(e)
		}
	}
	state := func(id string) string {
		t.Helper()
		var st string
		if e := s.st.Pool.QueryRow(ctx, `SELECT status FROM assistant_actions WHERE id=$1`, id).Scan(&st); e != nil {
			t.Fatal(e)
		}
		return st
	}
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	s.inner = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(entered)
		<-release
		w.WriteHeader(204)
	})
	add("out-concurrent", "pending")
	done := make(chan error, 1)
	go func() {
		_, _, _, _, e := s.executeAction(ctx, "itest-outcomes", "test-user", "operator", "out-concurrent", "web")
		done <- e
	}()
	<-entered
	if st := state("out-concurrent"); st != "executing" {
		t.Fatalf("claimed row=%s", st)
	}
	_, _, _, _, err = s.executeAction(ctx, "itest-outcomes", "test-user", "operator", "out-concurrent", "web")
	if !errors.Is(err, errActionGone) {
		t.Fatalf("concurrent confirm: %v", err)
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || state("out-concurrent") != "executed" {
		t.Fatal("duplicate execution or wrong terminal state")
	}

	for _, tc := range []struct {
		id   string
		code int
		want string
	}{{"out-success", 200, "executed"}, {"out-denied", 403, "failed"}, {"out-5xx", 500, "outcome_unknown"}} {
		add(tc.id, "pending")
		s.inner = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.code) })
		_, st, _, _, e := s.executeAction(ctx, "itest-outcomes", "test-user", "operator", tc.id, "web")
		if st != tc.want || state(tc.id) != tc.want {
			t.Fatalf("%s: %s %v", tc.id, st, e)
		}
		if tc.code == 500 && !errors.Is(e, errActionOutcomeUnknown) {
			t.Fatalf("5xx not unknown: %v", e)
		}
	}
	add("out-cancel", "executing")
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	s.inner = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("cancelled action executed") })
	st, _, _, err := s.runClaimedAction(cancelCtx, "itest-outcomes", "test-user", "operator", "out-cancel", "POST", "/v1/alerts/a/ack", "")
	if err != nil || st != "failed" || state("out-cancel") != "failed" {
		t.Fatalf("cancel result: %s %v", st, err)
	}

	add("out-during-cancel", "pending")
	duringCtx, duringCancel := context.WithCancel(ctx)
	s.inner = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { duringCancel(); w.WriteHeader(200) })
	_, st, _, _, err = s.executeAction(duringCtx, "itest-outcomes", "test-user", "operator", "out-during-cancel", "web")
	if st != "outcome_unknown" || !errors.Is(err, errActionOutcomeUnknown) || state("out-during-cancel") != "outcome_unknown" {
		t.Fatalf("cancel during effect: %s %v", st, err)
	}
	add("out-lost-record", "pending")
	s.inner = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.st.Pool.Exec(ctx, `DELETE FROM assistant_actions WHERE id='out-lost-record'`)
		w.WriteHeader(200)
	})
	_, st, _, _, err = s.executeAction(ctx, "itest-outcomes", "test-user", "operator", "out-lost-record", "web")
	if st != "outcome_unknown" || !errors.Is(err, errActionOutcomeUnknown) {
		t.Fatalf("lost record claimed success: %s %v", st, err)
	}

	add("out-crash", "executing")
	s.st.Pool.Exec(ctx, `UPDATE assistant_actions SET decided_at=now()-interval '11 minutes' WHERE id='out-crash'`)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/assistant/actions", s.listAssistantActions)
	w := call(mux, "itest-outcomes", "operator", "GET", "/v1/assistant/actions", "")
	if !strings.Contains(w.Body.String(), `"status":"outcome_unknown"`) {
		t.Fatalf("stale claim not surfaced: %s", w.Body.String())
	}
	_, _, _, _, err = s.executeAction(ctx, "itest-outcomes", "test-user", "operator", "out-crash", "web")
	if !errors.Is(err, errActionGone) {
		t.Fatalf("crash replay: %v", err)
	}
	// Simulate DB unavailable at the claim boundary, not a nonexistent action.
	closedStore, e := store.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	closedStore.Close()
	unavailable := &server{st: closedStore}
	_, _, _, _, err = unavailable.executeAction(ctx, "itest-outcomes", "test-user", "operator", "missing", "web")
	if !errors.Is(err, errActionUnavailable) {
		t.Fatalf("db failure mapped to gone: %v", err)
	}
}

func TestIntegrationAutorunUnknownStopsModel(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-out-auto")
	ctx := context.Background()
	s.st.Pool.Exec(ctx, `DELETE FROM assistant_actions WHERE tenant_id='itest-out-auto'`)
	t.Cleanup(func() { s.st.Pool.Exec(ctx, `DELETE FROM assistant_actions WHERE tenant_id='itest-out-auto'`) })
	s.inner = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
	fm := &fakeModel{script: []map[string]any{toolMsg("call-a", "api_request", `{"method":"POST","path":"/v1/alerts/a/ack","summary":"ack alert"}`)}}
	hs := httptest.NewServer(fm)
	defer hs.Close()
	got := s.runAgent(ctx, llm.Config{BaseURL: hs.URL, Model: "fake"}, "itest-out-auto", "test-user", "operator", []llm.Message{{Role: "user", Content: "acknowledge alert"}}, runCtx{via: "slack", autorun: true})
	if !errors.Is(got.err, errActionOutcomeUnknown) {
		t.Fatalf("unknown did not stop agent: %+v", got)
	}
	if len(fm.requests) != 1 {
		t.Fatalf("model continued after unknown: %d", len(fm.requests))
	}
	var n int
	s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM assistant_actions WHERE tenant_id='itest-out-auto' AND status='outcome_unknown'`).Scan(&n)
	if n != 1 {
		t.Fatalf("unknown rows: %d", n)
	}
}
