package main

import (
	"encoding/json"
	"net/http"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// POST /v1/alerts/{id}/assign (admin, operator): hand an unresolved alert to a person, or clear
// it with an empty user_id. The assignee must be an admin or operator of the same tenant, since
// viewers cannot act on alerts. Assignment is a note of ownership: it does not stop escalation
// (only acknowledging or resolving does) and it is recorded in the alert's note trail.
func (s *server) assignAlert(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	id, tenant := r.PathValue("id"), auth.Tenant(r)
	var name string
	if in.UserID != "" {
		if s.st.Pool.QueryRow(r.Context(), `SELECT display_name FROM users WHERE id=$1 AND tenant_id=$2 AND role IN ('admin','operator')`, in.UserID, tenant).Scan(&name) != nil {
			http.Error(w, "assignee must be an admin or operator of this tenant", 400)
			return
		}
	}
	tx, err := s.st.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer tx.Rollback(r.Context())
	var assignee *string
	if in.UserID != "" {
		assignee = &in.UserID
	}
	tag, err := tx.Exec(r.Context(),
		`UPDATE alerts SET assigned_to=$1, assigned_at=CASE WHEN $1::text IS NULL THEN NULL ELSE now() END
		 WHERE id=$2 AND tenant_id=$3 AND status IN ('open','acknowledged')`, assignee, id, tenant)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "alert not found in this tenant or already resolved", 409)
		return
	}
	note := "Unassigned"
	if in.UserID != "" {
		note = "Assigned to " + name
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO alert_comments(tenant_id, alert_id, author, body) VALUES($1,$2,$3,$4)`, tenant, id, auth.User(r), note); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit(r, "alert.assign", id, map[string]any{"assigned_to": in.UserID})
	writeJSON(w, 200, map[string]any{"id": id, "assigned_to": assignee})
}

// GET /v1/assignees (admin, operator): people an alert can be assigned to (id, name, role only).
func (s *server) listAssignees(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	rows, err := s.st.Pool.Query(r.Context(), `SELECT id, display_name, role FROM users WHERE tenant_id=$1 AND role IN ('admin','operator') ORDER BY display_name LIMIT 500`, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var id, name, role string
		if rows.Scan(&id, &name, &role) == nil {
			out = append(out, map[string]string{"id": id, "name": name, "role": role})
		}
	}
	writeJSON(w, 200, out)
}
