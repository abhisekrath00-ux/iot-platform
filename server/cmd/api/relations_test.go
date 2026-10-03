package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestIntegrationRelations(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-rl")
	seed(t, s, "itest-rl2")
	ctx := t.Context()
	clean := func() {
		for _, tn := range []string{"itest-rl", "itest-rl2"} {
			s.st.Pool.Exec(ctx, `DELETE FROM entity_relations WHERE tenant_id=$1`, tn)
			s.st.Pool.Exec(ctx, `UPDATE devices SET asset_id=NULL WHERE tenant_id=$1`, tn)
			s.st.Pool.Exec(ctx, `DELETE FROM assets WHERE tenant_id=$1`, tn)
		}
	}
	clean()
	t.Cleanup(clean)
	for _, id := range []string{"A", "B", "C", "D"} {
		s.st.Pool.Exec(ctx, `INSERT INTO assets(id,tenant_id,name) VALUES($1,'itest-rl',$1)`, "itest-rl-"+id)
	}
	s.st.Pool.Exec(ctx, `INSERT INTO assets(id,tenant_id,name) VALUES('itest-rl2-X','itest-rl2','X')`)
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/relations", s.listRelations)
	api.HandleFunc("POST /v1/relations", s.createRelation)
	api.HandleFunc("DELETE /v1/relations", s.deleteRelation)
	api.HandleFunc("GET /v1/relations/downstream", s.relationDownstream)
	api.HandleFunc("DELETE /v1/assets/{id}", s.deleteAsset)
	rel := func(from, name, to string) string {
		return `{"from_kind":"asset","from_id":"itest-rl-` + from + `","relation":"` + name + `","to_kind":"asset","to_id":"itest-rl-` + to + `"}`
	}
	if w := call(api, "itest-rl", "viewer", "POST", "/v1/relations", rel("A", "feeds", "B")); w.Code != 403 {
		t.Fatalf("viewer: %d", w.Code)
	}
	for _, bad := range []string{
		rel("A", "Feeds", "B"), // upper case
		rel("A", "feeds", "A"), // itself
		strings.Replace(rel("A", "feeds", "B"), "itest-rl-B", "itest-rl2-X", 1), // other tenant
		strings.Replace(rel("A", "feeds", "B"), "itest-rl-B", "nope", 1),
		strings.Replace(rel("A", "feeds", "B"), `"to_kind":"asset"`, `"to_kind":"gateway"`, 1),
	} {
		if w := call(api, "itest-rl", "operator", "POST", "/v1/relations", bad); w.Code != 400 {
			t.Errorf("accepted %s: %d", bad, w.Code)
		}
	}
	for _, r := range [][3]string{{"A", "feeds", "B"}, {"B", "feeds", "C"}, {"C", "feeds", "A"}, {"B", "feeds", "D"}, {"A", "backs_up", "D"}} {
		if w := call(api, "itest-rl", "operator", "POST", "/v1/relations", rel(r[0], r[1], r[2])); w.Code != 201 {
			t.Fatalf("create %v: %d %s", r, w.Code, w.Body.String())
		}
	}
	if w := call(api, "itest-rl", "operator", "POST", "/v1/relations", rel("A", "feeds", "B")); w.Code != 409 {
		t.Fatalf("duplicate: %d", w.Code)
	}
	// both directions show up for B
	w := call(api, "itest-rl", "viewer", "GET", "/v1/relations?kind=asset&id=itest-rl-B", "")
	var list []map[string]string
	json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != 200 || len(list) != 3 {
		t.Fatalf("B relations: %d %s", w.Code, w.Body.String())
	}
	// downstream of A over "feeds": B(1), C(2), D(2); the cycle back to A is not repeated and A is not listed
	w = call(api, "itest-rl", "viewer", "GET", "/v1/relations/downstream?kind=asset&id=itest-rl-A&relation=feeds", "")
	var down []map[string]any
	json.Unmarshal(w.Body.Bytes(), &down)
	if w.Code != 200 || len(down) != 3 || down[0]["id"] != "itest-rl-B" || down[0]["distance"].(float64) != 1 || down[1]["distance"].(float64) != 2 {
		t.Fatalf("downstream: %d %s", w.Code, w.Body.String())
	}
	// another relation name is a different graph
	w = call(api, "itest-rl", "viewer", "GET", "/v1/relations/downstream?kind=asset&id=itest-rl-A&relation=backs_up", "")
	if !strings.Contains(w.Body.String(), "itest-rl-D") || strings.Contains(w.Body.String(), "itest-rl-B") {
		t.Fatalf("backs_up graph: %s", w.Body.String())
	}
	// depth limit
	w = call(api, "itest-rl", "viewer", "GET", "/v1/relations/downstream?kind=asset&id=itest-rl-A&relation=feeds&depth=1", "")
	json.Unmarshal(w.Body.Bytes(), &down)
	if len(down) != 1 {
		t.Fatalf("depth 1: %s", w.Body.String())
	}
	if w := call(api, "itest-rl", "viewer", "GET", "/v1/relations/downstream?kind=asset&id=itest-rl-A&relation=feeds&depth=11", ""); w.Code != 400 {
		t.Fatalf("depth 11: %d", w.Code)
	}
	// another tenant sees nothing and cannot probe
	if w := call(api, "itest-rl2", "viewer", "GET", "/v1/relations/downstream?kind=asset&id=itest-rl-A&relation=feeds", ""); w.Code != 404 {
		t.Fatalf("cross-tenant: %d", w.Code)
	}
	// deleting a relation, then an asset removes its relations
	if w := call(api, "itest-rl", "operator", "DELETE", "/v1/relations", rel("B", "feeds", "D")); w.Code != 204 {
		t.Fatalf("delete relation: %d", w.Code)
	}
	if w := call(api, "itest-rl", "operator", "DELETE", "/v1/relations", rel("B", "feeds", "D")); w.Code != 404 {
		t.Fatalf("delete again: %d", w.Code)
	}
	if w := call(api, "itest-rl", "operator", "DELETE", "/v1/assets/itest-rl-C", ""); w.Code != 204 && w.Code != 200 {
		t.Fatalf("delete asset: %d", w.Code)
	}
	var n int
	s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM entity_relations WHERE tenant_id='itest-rl' AND (from_id='itest-rl-C' OR to_id='itest-rl-C')`).Scan(&n)
	if n != 0 {
		t.Fatalf("dangling relations after asset delete: %d", n)
	}
}
