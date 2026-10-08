package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestIntegrationAssistantRunsDurable(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-ar")
	seed(t, s, "itest-ar2")
	ctx := context.Background()
	s.st.Pool.Exec(ctx, `DELETE FROM assistant_runs WHERE tenant_id IN ('itest-ar','itest-ar2')`)

	id, replay, _, ok := s.beginRun(ctx, "itest-ar", "u1", "run-12345678")
	if !ok || replay != nil || id != "run-12345678" {
		t.Fatalf("first begin: %v %v %v", id, replay, ok)
	}
	// same id while running: not replayable, says running, does not run again
	if _, replay, st, ok := s.beginRun(ctx, "itest-ar", "u1", "run-12345678"); ok || replay != nil || st != "running" {
		t.Fatalf("running duplicate: %v %v %q", ok, replay, st)
	}
	s.finishRun("itest-ar", "run-12345678", "done", "", map[string]any{"reply": "hello"})
	_, replay, st, ok := s.beginRun(ctx, "itest-ar", "u1", "run-12345678")
	var got map[string]any
	if !ok || st != "done" || json.Unmarshal(replay, &got) != nil || got["reply"] != "hello" {
		t.Fatalf("replay: %v %q %s", ok, st, replay)
	}
	// another user and another tenant cannot read or replay it
	if _, replay, _, ok := s.beginRun(ctx, "itest-ar", "u2", "run-12345678"); ok || replay != nil {
		t.Fatal("another user got the run")
	}
	if _, replay, _, ok := s.beginRun(ctx, "itest-ar2", "u1", "run-12345678"); !ok || replay != nil {
		t.Fatal("tenants must not share run ids")
	}
	// a run in flight at restart becomes interrupted, and is never replayed
	s.beginRun(ctx, "itest-ar", "u1", "run-crashed01")
	markInterruptedRuns(ctx, s)
	_, replay, st, ok = s.beginRun(ctx, "itest-ar", "u1", "run-crashed01")
	if ok || replay != nil || st != "interrupted" {
		t.Fatalf("after restart: %v %v %q", ok, replay, st)
	}
	// finishing an already interrupted run must not overwrite it
	s.finishRun("itest-ar", "run-crashed01", "done", "", map[string]any{"reply": "late"})
	var status string
	s.st.Pool.QueryRow(ctx, `SELECT status FROM assistant_runs WHERE tenant_id='itest-ar' AND id='run-crashed01'`).Scan(&status)
	if status != "interrupted" {
		t.Fatalf("late finish overwrote: %s", status)
	}
	// a stale running row reads as interrupted
	s.beginRun(ctx, "itest-ar", "u1", "run-stale0001")
	s.st.Pool.Exec(ctx, `UPDATE assistant_runs SET started_at = now() - interval '1 hour' WHERE id='run-stale0001'`)
	if _, _, st, ok := s.beginRun(ctx, "itest-ar", "u1", "run-stale0001"); ok || st != "interrupted" {
		t.Fatalf("stale: %v %q", ok, st)
	}
}
