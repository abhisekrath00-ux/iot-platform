package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// Alert lifecycle: open -> acknowledged -> resolved, or open -> resolved;
// resolved is final. The rules live in the UPDATE ... WHERE status clauses so
// they hold under concurrent operators.
// ackAlert and resolveAlert transition one alert. The UPDATE re-checks the
// current status, so two operators acting at once cannot both win.
func (s *server) ackAlert(w http.ResponseWriter, r *http.Request) { s.transitionAlert(w, r, "ack") }
func (s *server) resolveAlert(w http.ResponseWriter, r *http.Request) {
	s.transitionAlert(w, r, "resolve")
}

func (s *server) transitionAlert(w http.ResponseWriter, r *http.Request, action string) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	id := r.PathValue("id")
	var q string
	var to string
	switch action {
	case "ack":
		to = "acknowledged"
		q = `UPDATE alerts SET status='acknowledged', acknowledged_by=$1, acknowledged_at=now()
		     WHERE id=$2 AND tenant_id=$3 AND status='open'`
	default:
		to = "resolved"
		q = `UPDATE alerts SET status='resolved', resolved_by=$1, resolved_at=now()
		     WHERE id=$2 AND tenant_id=$3 AND status IN ('open','acknowledged')`
	}
	tag, err := s.st.Pool.Exec(r.Context(), q, auth.User(r), id, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "alert not found in this tenant or not in a state that allows "+action, 409)
		return
	}
	s.audit(r, "alert."+action, id, nil)
	writeJSON(w, 200, map[string]any{"id": id, "status": to})
}

func (s *server) commentAlert(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	in.Body = strings.TrimSpace(in.Body)
	if in.Body == "" || len(in.Body) > 2000 {
		http.Error(w, "comment must be 1-2000 characters", 400)
		return
	}
	id := r.PathValue("id")
	tag, err := s.st.Pool.Exec(r.Context(),
		`INSERT INTO alert_comments(tenant_id, alert_id, author, body)
		 SELECT tenant_id, id, $1, $2 FROM alerts WHERE id=$3 AND tenant_id=$4`,
		auth.User(r), in.Body, id, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "alert not found", 404)
		return
	}
	s.audit(r, "alert.comment", id, nil)
	writeJSON(w, 201, map[string]any{"alert_id": id, "ok": true})
}

// getAlert returns one alert with lifecycle fields and its comment trail.
func (s *server) getAlert(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var sev, msg, st string
	var created time.Time
	var ackBy, resBy, asg *string
	var ackAt, resAt *time.Time
	err := s.st.Pool.QueryRow(r.Context(),
		`SELECT severity, message, status, created_at, acknowledged_by, acknowledged_at, resolved_by, resolved_at, assigned_to
		 FROM alerts WHERE id=$1 AND tenant_id=$2`, id, auth.Tenant(r)).
		Scan(&sev, &msg, &st, &created, &ackBy, &ackAt, &resBy, &resAt, &asg)
	if err != nil {
		http.Error(w, "alert not found", 404)
		return
	}
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT author, body, created_at FROM alert_comments WHERE alert_id=$1 AND tenant_id=$2 ORDER BY created_at, id`, id, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	comments := []map[string]any{}
	for rows.Next() {
		var a, b string
		var at time.Time
		rows.Scan(&a, &b, &at)
		comments = append(comments, map[string]any{"author": a, "body": b, "created_at": at})
	}
	writeJSON(w, 200, map[string]any{
		"id": id, "severity": sev, "message": msg, "status": st, "created_at": created,
		"acknowledged_by": ackBy, "acknowledged_at": ackAt, "resolved_by": resBy, "resolved_at": resAt, "assigned_to": asg,
		"comments": comments,
	})
}
