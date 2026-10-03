package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
)

const maxFragmentsPerTenant = 50

func (s *server) listFragments(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.Pool.Query(r.Context(), `SELECT id,name,jsonb_array_length(graph->'nodes') FROM flow_fragments WHERE tenant_id=$1 ORDER BY name LIMIT 100`, auth.Tenant(r))
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name string
		var n int
		if rows.Scan(&id, &name, &n) == nil {
			out = append(out, map[string]any{"id": id, "name": name, "nodes": n})
		}
	}
	writeJSON(w, 200, out)
}

// POST /v1/flow-fragments {name, graph}: the graph must pass flow.ValidateFragment. Function (JavaScript)
// nodes are admin-only everywhere, so a fragment holding one can only be saved by an admin.
func (s *server) createFragment(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		Name  string     `json:"name"`
		Graph flow.Graph `json:"graph"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&in) != nil {
		http.Error(w, "bad json", 400)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 80 || strings.ContainsAny(in.Name, "<>\x00") {
		http.Error(w, "name required (up to 80 characters)", 400)
		return
	}
	if _, err := flow.ValidateFragment(in.Graph); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	for _, n := range in.Graph.Nodes {
		if (n.Type == "function" || n.Type == "http" || n.Type == "control") && auth.Role(r) != "admin" {
			http.Error(w, n.Type+" nodes in a fragment need an admin", 403)
			return
		}
	}
	var n int
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM flow_fragments WHERE tenant_id=$1`, auth.Tenant(r)).Scan(&n)
	if n >= maxFragmentsPerTenant {
		http.Error(w, "too many fragments", 409)
		return
	}
	body, _ := json.Marshal(in.Graph)
	id := uuid.NewString()
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO flow_fragments(id,tenant_id,name,graph,created_by) VALUES($1,$2,$3,$4,$5)`, id, auth.Tenant(r), in.Name, body, auth.User(r)); err != nil {
		http.Error(w, "a fragment with that name exists", 409)
		return
	}
	s.audit(r, "flow.fragment.create", id, map[string]any{"name": in.Name, "nodes": len(in.Graph.Nodes)})
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *server) deleteFragment(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	ct, err := s.st.Pool.Exec(r.Context(), `DELETE FROM flow_fragments WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), auth.Tenant(r))
	if err != nil || ct.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "flow.fragment.delete", r.PathValue("id"), nil)
	w.WriteHeader(204)
}

// POST /v1/flow-fragments/{id}/instantiate {prefix, x, y}: copies of the nodes and edges with unique ids,
// plus the entry and exit ids to wire. Nothing is saved; the editor adds them to the draft.
func (s *server) instantiateFragment(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Prefix string  `json:"prefix"`
		X      float64 `json:"x"`
		Y      float64 `json:"y"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		http.Error(w, "bad json", 400)
		return
	}
	var raw []byte
	if s.st.Pool.QueryRow(r.Context(), `SELECT graph FROM flow_fragments WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), auth.Tenant(r)).Scan(&raw) != nil {
		http.Error(w, "not found", 404)
		return
	}
	var g flow.Graph
	if json.Unmarshal(raw, &g) != nil {
		http.Error(w, "stored fragment invalid", 500)
		return
	}
	nodes, edges, entry, exits, err := flow.InstantiateFragment(g, in.Prefix, in.X, in.Y)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, 200, map[string]any{"nodes": nodes, "edges": edges, "entry": entry, "exits": exits})
}
