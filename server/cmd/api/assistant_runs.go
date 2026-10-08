package main

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"time"

	"github.com/google/uuid"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// Durable assistant runs. Every chat run gets a row. The client may send its own id in X-Run-Id; sending the same id
// again returns the stored answer instead of running the model a second time. A run that was in flight when the
// server stopped becomes "interrupted": the model loop is not resumed (a half-finished loop cannot be continued
// safely), and no action is replayed. Changes still need the user's confirmation, and what happened to each action
// is in the action outcome record.

var runIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{7,63}$`)

// runStaleAfter is how long a "running" row is believed before it is shown as interrupted.
const runStaleAfter = 15 * time.Minute

// beginRun registers a run. replay is the stored answer to send instead of running again (nil to run); when the id
// is already taken by a run that cannot be replayed, status explains why and ok is false.
func (s *server) beginRun(ctx context.Context, tenant, user, id string) (runID string, replay json.RawMessage, status string, ok bool) {
	if id == "" {
		id = uuid.NewString()
	}
	tag, err := s.st.Pool.Exec(ctx, `INSERT INTO assistant_runs(id,tenant_id,user_id,status) VALUES($1,$2,$3,'running') ON CONFLICT DO NOTHING`, id, tenant, user)
	if err != nil {
		return id, nil, "", true // never block the assistant because bookkeeping failed
	}
	if tag.RowsAffected() == 1 {
		return id, nil, "", true
	}
	var st, owner string
	var res []byte
	var started time.Time
	if err := s.st.Pool.QueryRow(ctx, `SELECT status,user_id,result,started_at FROM assistant_runs WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&st, &owner, &res, &started); err != nil || owner != user {
		return id, nil, "run id already used", false
	}
	if st == "running" && time.Since(started) > runStaleAfter {
		st = "interrupted"
	}
	if st == "done" && len(res) > 0 {
		return id, res, "done", true
	}
	return id, nil, st, false
}

func (s *server) finishRun(tenant, id, status, errMsg string, result any) {
	var b []byte
	if result != nil {
		b, _ = json.Marshal(result)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.st.Pool.Exec(ctx, `UPDATE assistant_runs SET status=$3, error=$4, result=$5, finished_at=now() WHERE tenant_id=$1 AND id=$2 AND status='running'`, tenant, id, status, truncStr(errMsg, 300), b)
}

// markInterruptedRuns runs at startup: any run still "running" belonged to a process that is gone.
func markInterruptedRuns(ctx context.Context, s *server) {
	s.st.Pool.Exec(ctx, `UPDATE assistant_runs SET status='interrupted', error='the server stopped before this run finished', finished_at=now() WHERE status='running'`)
}

// GET /v1/assistant/runs/{id}: the caller's own run: status and, when finished, the stored answer.
func (s *server) getAssistantRun(w http.ResponseWriter, r *http.Request) {
	if auth.ViaKey(r) {
		http.Error(w, "the assistant needs an interactive user session", 403)
		return
	}
	var st, errMsg string
	var res []byte
	var started time.Time
	err := s.st.Pool.QueryRow(r.Context(), `SELECT status,error,result,started_at FROM assistant_runs WHERE tenant_id=$1 AND id=$2 AND user_id=$3`,
		auth.Tenant(r), r.PathValue("id"), auth.User(r)).Scan(&st, &errMsg, &res, &started)
	if err != nil {
		http.Error(w, "no such run", 404)
		return
	}
	if st == "running" && time.Since(started) > runStaleAfter {
		st, errMsg = "interrupted", "no result was stored; the server may have stopped"
	}
	out := map[string]any{"id": r.PathValue("id"), "status": st, "started_at": started}
	if errMsg != "" {
		out["error"] = errMsg
	}
	if len(res) > 0 {
		out["result"] = json.RawMessage(res)
	}
	if st == "interrupted" || st == "failed" {
		out["note"] = "The model loop is not resumed. Nothing changed without your confirmation; check pending actions before asking again."
	}
	writeJSON(w, 200, out)
}
