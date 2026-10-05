package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// GET /v1/ai/activity (admin, interactive session): what the AI did in this tenant. It is a view
// over the audit log and the assistant's action table, so it cannot disagree with them: counts for
// the last N days (default 30), and the most recent runs with the tools each one used.
func (s *server) aiActivity(w http.ResponseWriter, r *http.Request) {
	if auth.ViaKey(r) || !requireRole(w, r, "admin") {
		return
	}
	tenant := auth.Tenant(r)
	days := 30
	if d := r.URL.Query().Get("days"); d != "" {
		if n, err := atoiBounded(d, 1, 365); err == nil {
			days = n
		}
	}
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	var runs, failed, cancelled, toolCalls, proposed, confirms, rejects, autoruns int
	s.st.Pool.QueryRow(r.Context(), `SELECT
	  count(*) FILTER (WHERE action='assistant.run'),
	  count(*) FILTER (WHERE action='assistant.run' AND detail ? 'error'),
	  count(*) FILTER (WHERE action='assistant.run' AND detail->>'cancelled'='true'),
	  COALESCE(sum((detail->>'tool_calls')::int) FILTER (WHERE action IN ('assistant.run','assistant.channel_run')),0),
	  COALESCE(sum((detail->>'proposed_changes')::int) FILTER (WHERE action IN ('assistant.run','assistant.channel_run')),0),
	  count(*) FILTER (WHERE action='assistant.confirm'),
	  count(*) FILTER (WHERE action='assistant.reject'),
	  count(*) FILTER (WHERE action='assistant.autorun')
	  FROM audit_log WHERE tenant_id=$1 AND at >= $2 AND action LIKE 'assistant.%'`, tenant, since).
		Scan(&runs, &failed, &cancelled, &toolCalls, &proposed, &confirms, &rejects, &autoruns)
	var refused int
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM audit_log a, jsonb_array_elements(CASE WHEN jsonb_typeof(a.detail->'trace')='array' THEN a.detail->'trace' ELSE '[]'::jsonb END) t
	  WHERE a.tenant_id=$1 AND a.at >= $2 AND a.action='assistant.run' AND t->>'status'='refused'`, tenant, since).Scan(&refused)
	var pendingNow int
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM assistant_actions WHERE tenant_id=$1 AND status='pending' AND created_at > now() - interval '30 minutes'`, tenant).Scan(&pendingNow)

	rows, err := s.st.Pool.Query(r.Context(), `SELECT at, actor, action, target, detail FROM audit_log
	  WHERE tenant_id=$1 AND action IN ('assistant.run','assistant.channel_run','assistant.confirm','assistant.reject','assistant.autorun','assistant.inbound_refused')
	  ORDER BY at DESC LIMIT 100`, tenant)
	if err != nil {
		http.Error(w, "could not read the activity", 500)
		return
	}
	defer rows.Close()
	recent := []map[string]any{}
	for rows.Next() {
		var at time.Time
		var actor, action string
		var target *string
		var detail json.RawMessage
		rows.Scan(&at, &actor, &action, &target, &detail)
		recent = append(recent, map[string]any{"at": at, "user": actor, "action": action, "target": target, "detail": detail})
	}
	writeJSON(w, 200, map[string]any{"days": days, "runs": runs, "failed_runs": failed, "cancelled_runs": cancelled, "tool_calls": toolCalls,
		"refused_tool_calls": refused, "changes_proposed": proposed, "changes_confirmed": confirms, "changes_rejected": rejects,
		"changes_autorun": autoruns, "pending_now": pendingNow, "recent": recent})
}

func atoiBounded(s string, lo, hi int) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' || n > 100000 {
			return 0, http.ErrNotSupported
		}
		n = n*10 + int(c-'0')
	}
	if n < lo || n > hi {
		return 0, http.ErrNotSupported
	}
	return n, nil
}
