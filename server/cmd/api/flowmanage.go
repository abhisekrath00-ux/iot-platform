package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
	"github.com/google/uuid"
)

func validFlowName(name string) bool {
	return name != "" && len(name) <= 128 && !strings.ContainsAny(name, "<>\x00")
}

// getFlowVersion returns one stored version, so the editor opens exactly what
// was saved (the list endpoint only carries the first definition).
func (s *server) getFlowVersion(w http.ResponseWriter, r *http.Request) {
	ver, err := strconv.Atoi(r.PathValue("version"))
	if err != nil {
		http.Error(w, "bad version", 400)
		return
	}
	var def []byte
	var status string
	if s.st.Pool.QueryRow(r.Context(),
		`SELECT definition, status FROM flow_versions WHERE flow_id=$1 AND tenant_id=$2 AND version=$3`,
		r.PathValue("id"), auth.Tenant(r), ver).Scan(&def, &status) != nil {
		http.Error(w, "version not found", 404)
		return
	}
	writeJSON(w, 200, map[string]any{"version": ver, "status": status, "definition": json.RawMessage(def)})
}

// patchFlow renames a flow and/or enables or disables it. Disabling stops it
// from running without deleting anything.
func (s *server) patchFlow(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		Name    *string `json:"name"`
		Enabled *bool   `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil || (in.Name == nil && in.Enabled == nil) {
		http.Error(w, "name or enabled required", 400)
		return
	}
	id := r.PathValue("id")
	if !s.flowOwned(r, id) {
		http.Error(w, "flow not found", 404)
		return
	}
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if !validFlowName(n) {
			http.Error(w, "name invalid", 400)
			return
		}
		if _, err := s.st.Pool.Exec(r.Context(), `UPDATE flows SET name=$1 WHERE id=$2 AND tenant_id=$3`, n, id, auth.Tenant(r)); err != nil {
			http.Error(w, "db", 500)
			return
		}
		s.audit(r, "flow.rename", id, map[string]any{"name": n})
	}
	if in.Enabled != nil {
		if _, err := s.st.Pool.Exec(r.Context(), `UPDATE flows SET enabled=$1 WHERE id=$2 AND tenant_id=$3`, *in.Enabled, id, auth.Tenant(r)); err != nil {
			http.Error(w, "db", 500)
			return
		}
		s.audit(r, "flow.enable", id, map[string]any{"enabled": *in.Enabled})
	}
	writeJSON(w, 200, map[string]any{"id": id})
}

// deleteFlow removes a flow with its versions and run history. Admin only; the
// audit entry keeps the name.
func (s *server) deleteFlow(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	id := r.PathValue("id")
	var name string
	if s.st.Pool.QueryRow(r.Context(), `SELECT name FROM flows WHERE id=$1 AND tenant_id=$2`, id, auth.Tenant(r)).Scan(&name) != nil {
		http.Error(w, "flow not found", 404)
		return
	}
	tx, err := s.st.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer tx.Rollback(r.Context())
	// the published pointer references a version, so clear it before the cascade
	if _, err := tx.Exec(r.Context(), `UPDATE flows SET published_version_id=NULL WHERE id=$1`, id); err != nil {
		http.Error(w, "db", 500)
		return
	}
	if _, err := tx.Exec(r.Context(), `DELETE FROM flows WHERE id=$1 AND tenant_id=$2`, id, auth.Tenant(r)); err != nil {
		http.Error(w, "db", 500)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "flow.delete", id, map[string]any{"name": name})
	writeJSON(w, 200, map[string]any{"deleted": id})
}

// duplicateFlow copies a flow's newest version into a new UNPUBLISHED draft.
func (s *server) duplicateFlow(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in)
	src := r.PathValue("id")
	var name string
	var def []byte
	if s.st.Pool.QueryRow(r.Context(),
		`SELECT f.name, v.definition FROM flows f JOIN flow_versions v ON v.flow_id=f.id
		 WHERE f.id=$1 AND f.tenant_id=$2 ORDER BY v.version DESC LIMIT 1`, src, auth.Tenant(r)).Scan(&name, &def) != nil {
		http.Error(w, "flow not found", 404)
		return
	}
	var d flow.Definition
	if json.Unmarshal(def, &d) != nil {
		http.Error(w, "stored definition invalid", 500)
		return
	}
	if !s.gateFunctionNodes(w, r, d) {
		return
	}
	n := strings.TrimSpace(in.Name)
	if n == "" {
		n = name + " (copy)"
	}
	if !validFlowName(n) {
		http.Error(w, "name invalid", 400)
		return
	}
	id, verID := uuid.NewString(), uuid.NewString()
	tx, err := s.st.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(), `INSERT INTO flows(id,tenant_id,name,definition,created_by) VALUES($1,$2,$3,$4,$5)`,
		id, auth.Tenant(r), n, def, auth.User(r)); err != nil {
		http.Error(w, "db", 500)
		return
	}
	if _, err := tx.Exec(r.Context(), `INSERT INTO flow_versions(id,flow_id,tenant_id,version,definition,status,created_by)
		VALUES($1,$2,$3,1,$4,'draft',$5)`, verID, id, auth.Tenant(r), def, auth.User(r)); err != nil {
		http.Error(w, "db", 500)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "flow.duplicate", id, map[string]any{"from": src, "name": n})
	writeJSON(w, 201, map[string]any{"id": id, "version": 1, "status": "draft"})
}
