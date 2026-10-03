package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	s.st.Pool.Exec(r.Context(), `INSERT INTO flow_fragment_versions(fragment_id,version,graph,created_by) VALUES($1,1,$2,$3)`, id, body, auth.User(r))
	s.audit(r, "flow.fragment.create", id, map[string]any{"name": in.Name, "nodes": len(in.Graph.Nodes)})
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *server) deleteFragment(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var used bool
	s.st.Pool.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM flow_versions v CROSS JOIN LATERAL jsonb_array_elements(COALESCE(v.definition->'source'->'nodes','[]'::jsonb)) n
		WHERE v.tenant_id=$1 AND n->>'type'='subflow' AND n->>'fragment_id'=$2)`, auth.Tenant(r), r.PathValue("id")).Scan(&used)
	if used {
		http.Error(w, "a flow still references this fragment", 409)
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

// resolveFragment loads a tenant's fragment at a version (0 = current) for subflow expansion.
func (s *server) fragmentResolver(ctx context.Context, tenant string) flow.FragmentResolver {
	return func(id string, version int) (flow.Graph, int, error) {
		var raw []byte
		var cur int
		if s.st.Pool.QueryRow(ctx, `SELECT version FROM flow_fragments WHERE id=$1 AND tenant_id=$2`, id, tenant).Scan(&cur) != nil {
			return flow.Graph{}, 0, fmt.Errorf("fragment not found")
		}
		if version <= 0 {
			version = cur
		}
		if s.st.Pool.QueryRow(ctx, `SELECT graph FROM flow_fragment_versions WHERE fragment_id=$1 AND version=$2`, id, version).Scan(&raw) != nil {
			return flow.Graph{}, 0, fmt.Errorf("fragment version %d not found", version)
		}
		var g flow.Graph
		if json.Unmarshal(raw, &g) != nil {
			return flow.Graph{}, 0, fmt.Errorf("stored fragment invalid")
		}
		return g, version, nil
	}
}

// expandDefinition turns a draft that uses subflow nodes into the stored shape: Source keeps the references
// (pinned), Graph holds the expansion that runs. A client-supplied Source is never trusted.
func (s *server) expandDefinition(ctx context.Context, tenant string, d *flow.Definition) error {
	d.Source = nil
	if !flow.HasSubflows(d.Graph) {
		return nil
	}
	exp, pinned, err := flow.ExpandSubflows(*d.Graph, s.fragmentResolver(ctx, tenant))
	if err != nil {
		return err
	}
	d.Source, d.Graph = &pinned, &exp
	return nil
}

// PUT /v1/flow-fragments/{id} {graph}: saves a new version. Flows keep running the version they pinned.
func (s *server) updateFragment(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		Graph flow.Graph `json:"graph"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&in) != nil {
		http.Error(w, "bad json", 400)
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
	body, _ := json.Marshal(in.Graph)
	tx, err := s.st.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer tx.Rollback(r.Context())
	var ver int
	if tx.QueryRow(r.Context(), `UPDATE flow_fragments SET graph=$3, version=version+1 WHERE id=$1 AND tenant_id=$2 RETURNING version`, r.PathValue("id"), auth.Tenant(r), body).Scan(&ver) != nil {
		http.Error(w, "not found", 404)
		return
	}
	if _, err := tx.Exec(r.Context(), `INSERT INTO flow_fragment_versions(fragment_id,version,graph,created_by) VALUES($1,$2,$3,$4)`, r.PathValue("id"), ver, body, auth.User(r)); err != nil || tx.Commit(r.Context()) != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "flow.fragment.update", r.PathValue("id"), map[string]any{"version": ver})
	writeJSON(w, 200, map[string]any{"version": ver})
}

// GET /v1/flow-fragments/{id}/usage lists the flows whose latest version references this fragment, with the
// pinned version and whether it is behind the current one.
func (s *server) fragmentUsage(w http.ResponseWriter, r *http.Request) {
	id, t := r.PathValue("id"), auth.Tenant(r)
	var cur int
	if s.st.Pool.QueryRow(r.Context(), `SELECT version FROM flow_fragments WHERE id=$1 AND tenant_id=$2`, id, t).Scan(&cur) != nil {
		http.Error(w, "not found", 404)
		return
	}
	rows, err := s.st.Pool.Query(r.Context(), `
		SELECT f.id, f.name, v.version, (f.published_version_id = v.id), (n->>'fragment_version')::int
		FROM flow_versions v JOIN flows f ON f.id=v.flow_id
		CROSS JOIN LATERAL jsonb_array_elements(v.definition->'source'->'nodes') n
		WHERE v.tenant_id=$1 AND n->>'type'='subflow' AND n->>'fragment_id'=$2
		  AND (v.version = (SELECT max(version) FROM flow_versions WHERE flow_id=f.id) OR f.published_version_id=v.id)
		ORDER BY f.name, v.version DESC LIMIT 200`, t, id)
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var fid, name string
		var ver, pin int
		var pub bool
		if rows.Scan(&fid, &name, &ver, &pub, &pin) == nil {
			out = append(out, map[string]any{"flow_id": fid, "flow": name, "flow_version": ver, "published": pub, "pinned_version": pin, "out_of_date": pin < cur})
		}
	}
	writeJSON(w, 200, map[string]any{"current_version": cur, "flows": out})
}

// POST /v1/flows/{id}/refresh-subflows creates a new draft from the flow's latest version with every
// subflow pin moved to the current fragment version. It never publishes.
func (s *server) refreshSubflows(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	id := r.PathValue("id")
	if !s.flowOwned(r, id) {
		http.Error(w, "flow not found", 404)
		return
	}
	var raw []byte
	if s.st.Pool.QueryRow(r.Context(), `SELECT definition FROM flow_versions WHERE flow_id=$1 AND tenant_id=$2 ORDER BY version DESC LIMIT 1`, id, auth.Tenant(r)).Scan(&raw) != nil {
		http.Error(w, "flow has no versions", 404)
		return
	}
	var d flow.Definition
	if json.Unmarshal(raw, &d) != nil || d.Source == nil {
		http.Error(w, "flow does not use subflows", 400)
		return
	}
	src := flow.Graph{Nodes: append([]flow.Node(nil), d.Source.Nodes...), Edges: d.Source.Edges}
	for i := range src.Nodes {
		if src.Nodes[i].Type == "subflow" {
			src.Nodes[i].FragmentVersion = 0
		}
	}
	d.Graph = &src
	body, _ := json.Marshal(map[string]any{"definition": d})
	r2 := r.Clone(r.Context())
	r2.Body = io.NopCloser(bytes.NewReader(body))
	s.createFlowDraft(w, r2)
}
