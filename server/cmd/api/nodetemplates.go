package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// Custom node types: admin-defined function-node templates. A template is a name, a description and code. It
// is a convenience for authoring, not a new way to run code: using one in the editor copies its code into
// an ordinary function node, which is checked and sandboxed exactly like any other (admin only, the
// function_nodes feature on, 50 ms and memory limits). Editing or deleting a template never changes flows
// that already copied it. Managing templates needs the same gate as writing function code.

const (
	maxNodeTemplates = 100
	maxTemplateCode  = 4000 // same cap as a function node
)

type nodeTemplateIn struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Code        string `json:"code"`
}

func (s *server) templateGate(w http.ResponseWriter, r *http.Request) bool {
	if auth.ViaKey(r) {
		http.Error(w, "not available to API keys or the assistant", http.StatusForbidden)
		return false
	}
	if !s.functionNodesAllowed(r) {
		http.Error(w, "custom node types require an admin and the function_nodes feature enabled for this tenant", http.StatusForbidden)
		return false
	}
	return true
}

func (s *server) validateTemplate(w http.ResponseWriter, in *nodeTemplateIn) bool {
	in.Name, in.Description = strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)
	if in.Name == "" || len(in.Name) > 60 || strings.ContainsAny(in.Name, "\r\n") {
		http.Error(w, "a name of up to 60 characters is required", 400)
		return false
	}
	if len(in.Description) > 300 {
		http.Error(w, "description is limited to 300 characters", 400)
		return false
	}
	if strings.TrimSpace(in.Code) == "" || len(in.Code) > maxTemplateCode {
		http.Error(w, "code is required, up to 4000 bytes", 400)
		return false
	}
	if err := fnRunner.Check(in.Code); err != nil {
		http.Error(w, "code: "+err.Error(), 400)
		return false
	}
	return true
}

func (s *server) listNodeTemplates(w http.ResponseWriter, r *http.Request) {
	if !s.templateGate(w, r) {
		return
	}
	rows, err := s.st.Pool.Query(r.Context(), `SELECT id,name,description,code,updated_at FROM node_templates WHERE tenant_id=$1 ORDER BY lower(name)`, auth.Tenant(r))
	if err != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, desc, code string
		var at time.Time
		if rows.Scan(&id, &name, &desc, &code, &at) == nil {
			out = append(out, map[string]any{"id": id, "name": name, "description": desc, "code": code, "updated_at": at})
		}
	}
	writeJSON(w, 200, out)
}

func (s *server) createNodeTemplate(w http.ResponseWriter, r *http.Request) {
	if !s.templateGate(w, r) {
		return
	}
	var in nodeTemplateIn
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in) != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if !s.validateTemplate(w, &in) {
		return
	}
	t := auth.Tenant(r)
	var n int
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM node_templates WHERE tenant_id=$1`, t).Scan(&n)
	if n >= maxNodeTemplates {
		http.Error(w, "limit of 100 custom node types reached", http.StatusConflict)
		return
	}
	id := "nt-" + uuid.NewString()
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO node_templates(id,tenant_id,name,description,code,created_by) VALUES($1,$2,$3,$4,$5,$6)`,
		id, t, in.Name, in.Description, in.Code, auth.User(r)); err != nil {
		http.Error(w, "a custom node type with that name already exists", http.StatusConflict)
		return
	}
	s.audit(r, "node_template.create", id, map[string]any{"name": in.Name})
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *server) updateNodeTemplate(w http.ResponseWriter, r *http.Request) {
	if !s.templateGate(w, r) {
		return
	}
	var in nodeTemplateIn
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in) != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if !s.validateTemplate(w, &in) {
		return
	}
	tag, err := s.st.Pool.Exec(r.Context(), `UPDATE node_templates SET name=$3, description=$4, code=$5, updated_at=now() WHERE id=$1 AND tenant_id=$2`,
		r.PathValue("id"), auth.Tenant(r), in.Name, in.Description, in.Code)
	if err != nil {
		http.Error(w, "a custom node type with that name already exists", http.StatusConflict)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "node_template.update", r.PathValue("id"), map[string]any{"name": in.Name})
	w.WriteHeader(204)
}

func (s *server) deleteNodeTemplate(w http.ResponseWriter, r *http.Request) {
	if !s.templateGate(w, r) {
		return
	}
	tag, _ := s.st.Pool.Exec(r.Context(), `DELETE FROM node_templates WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), auth.Tenant(r))
	if tag.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "node_template.delete", r.PathValue("id"), nil)
	w.WriteHeader(204)
}
