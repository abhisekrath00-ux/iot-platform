package main

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

var relationName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

const maxRelationsPerTenant = 20000

type relation struct {
	FromKind string `json:"from_kind"`
	FromID   string `json:"from_id"`
	Relation string `json:"relation"`
	ToKind   string `json:"to_kind"`
	ToID     string `json:"to_id"`
}

func (s *server) entityExists(r *http.Request, kind, id string) bool {
	table := "assets"
	if kind == "device" {
		table = "devices"
	} else if kind != "asset" {
		return false
	}
	var n int
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM `+table+` WHERE id=$1 AND tenant_id=$2`, id, auth.Tenant(r)).Scan(&n)
	return n > 0
}

// GET /v1/relations?kind=&id= : every relation touching one entity, both directions.
func (s *server) listRelations(w http.ResponseWriter, r *http.Request) {
	kind, id := r.URL.Query().Get("kind"), r.URL.Query().Get("id")
	if (kind != "asset" && kind != "device") || id == "" {
		http.Error(w, "kind (asset or device) and id are required", 400)
		return
	}
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT from_kind, from_id, relation, to_kind, to_id FROM entity_relations
		 WHERE tenant_id=$1 AND ((from_kind=$2 AND from_id=$3) OR (to_kind=$2 AND to_id=$3)) ORDER BY relation, from_id, to_id LIMIT 500`,
		auth.Tenant(r), kind, id)
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer rows.Close()
	out := []relation{}
	for rows.Next() {
		var x relation
		if rows.Scan(&x.FromKind, &x.FromID, &x.Relation, &x.ToKind, &x.ToID) == nil {
			out = append(out, x)
		}
	}
	writeJSON(w, 200, out)
}

// POST /v1/relations (admin or operator)
func (s *server) createRelation(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in relation
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if !relationName.MatchString(in.Relation) {
		http.Error(w, "relation must be lower-case letters, digits and underscores, starting with a letter (32 characters at most)", 400)
		return
	}
	if in.FromKind == in.ToKind && in.FromID == in.ToID {
		http.Error(w, "an entity cannot relate to itself", 400)
		return
	}
	if !s.entityExists(r, in.FromKind, in.FromID) || !s.entityExists(r, in.ToKind, in.ToID) {
		http.Error(w, "both ends must exist in this tenant", 400)
		return
	}
	var n int
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM entity_relations WHERE tenant_id=$1`, auth.Tenant(r)).Scan(&n)
	if n >= maxRelationsPerTenant {
		http.Error(w, "relation limit reached", 409)
		return
	}
	tag, err := s.st.Pool.Exec(r.Context(),
		`INSERT INTO entity_relations(tenant_id,from_kind,from_id,relation,to_kind,to_id,created_by) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`,
		auth.Tenant(r), in.FromKind, in.FromID, in.Relation, in.ToKind, in.ToID, auth.User(r))
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "relation already exists", 409)
		return
	}
	s.audit(r, "relation.create", in.FromKind+":"+in.FromID, map[string]any{"relation": in.Relation, "to": in.ToKind + ":" + in.ToID})
	writeJSON(w, 201, in)
}

// DELETE /v1/relations (admin or operator): body names the relation.
func (s *server) deleteRelation(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in relation
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	tag, err := s.st.Pool.Exec(r.Context(),
		`DELETE FROM entity_relations WHERE tenant_id=$1 AND from_kind=$2 AND from_id=$3 AND relation=$4 AND to_kind=$5 AND to_id=$6`,
		auth.Tenant(r), in.FromKind, in.FromID, in.Relation, in.ToKind, in.ToID)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "relation.delete", in.FromKind+":"+in.FromID, map[string]any{"relation": in.Relation, "to": in.ToKind + ":" + in.ToID})
	w.WriteHeader(204)
}

// GET /v1/relations/downstream?kind=&id=&relation=feeds&depth=5
// Everything reachable by following one relation forward: what depends on this entity. Cycles are
// handled (each entity appears once, at its shortest distance). Depth is capped at 10, results at 500.
func (s *server) relationDownstream(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind, id, rel := q.Get("kind"), q.Get("id"), q.Get("relation")
	depth := 5
	if v := q.Get("depth"); v != "" {
		d, err := strconv.Atoi(v)
		if err != nil || d < 1 || d > 10 {
			http.Error(w, "depth must be 1-10", 400)
			return
		}
		depth = d
	}
	if (kind != "asset" && kind != "device") || id == "" || !relationName.MatchString(rel) {
		http.Error(w, "kind, id and relation are required", 400)
		return
	}
	if !s.entityExists(r, kind, id) {
		http.Error(w, "not found", 404)
		return
	}
	rows, err := s.st.Pool.Query(r.Context(),
		`WITH RECURSIVE walk(kind, id, dist, path) AS (
		   SELECT to_kind, to_id, 1, ARRAY[from_kind||':'||from_id, to_kind||':'||to_id]
		     FROM entity_relations WHERE tenant_id=$1 AND relation=$4 AND from_kind=$2 AND from_id=$3
		   UNION ALL
		   SELECT e.to_kind, e.to_id, w.dist+1, w.path || (e.to_kind||':'||e.to_id)
		     FROM entity_relations e JOIN walk w ON e.from_kind=w.kind AND e.from_id=w.id
		    WHERE e.tenant_id=$1 AND e.relation=$4 AND w.dist < $5 AND NOT (e.to_kind||':'||e.to_id) = ANY(w.path)
		 )
		 SELECT kind, id, min(dist) AS dist FROM walk
		  WHERE NOT (kind=$2 AND id=$3) GROUP BY kind, id ORDER BY min(dist), kind, id LIMIT 500`,
		auth.Tenant(r), kind, id, rel, depth)
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var k, i string
		var d int
		if rows.Scan(&k, &i, &d) == nil {
			out = append(out, map[string]any{"kind": k, "id": i, "distance": d})
		}
	}
	writeJSON(w, 200, out)
}
