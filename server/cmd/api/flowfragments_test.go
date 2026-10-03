package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestIntegrationFlowFragments(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-ff")
	seed(t, s, "itest-ff2")
	ctx := t.Context()
	clean := func() {
		s.st.Pool.Exec(ctx, `DELETE FROM flow_fragments WHERE tenant_id IN ('itest-ff','itest-ff2')`)
		s.st.Pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id IN ('itest-ff','itest-ff2')`)
	}
	clean()
	t.Cleanup(clean)
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/flow-fragments", s.listFragments)
	api.HandleFunc("POST /v1/flow-fragments", s.createFragment)
	api.HandleFunc("DELETE /v1/flow-fragments/{id}", s.deleteFragment)
	api.HandleFunc("POST /v1/flow-fragments/{id}/instantiate", s.instantiateFragment)
	good := `{"name":"Scale and test","graph":{"nodes":[{"id":"r","type":"range","in_min":0,"in_max":100,"out_min":0,"out_max":1,"clamp":true},{"id":"c","type":"condition","op":">","value":0.5}],"edges":[{"from":"r","to":"c"}]}}`
	w := call(api, "itest-ff", "operator", "POST", "/v1/flow-fragments", good)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var c struct{ ID string }
	json.Unmarshal(w.Body.Bytes(), &c)
	if call(api, "itest-ff", "operator", "POST", "/v1/flow-fragments", good).Code != 409 {
		t.Fatal("duplicate name accepted")
	}
	if call(api, "itest-ff", "viewer", "POST", "/v1/flow-fragments", good).Code != 403 {
		t.Fatal("viewer created")
	}
	two := `{"name":"Two entries","graph":{"nodes":[{"id":"a","type":"condition","op":">"},{"id":"b","type":"condition","op":">"}]}}`
	if call(api, "itest-ff", "operator", "POST", "/v1/flow-fragments", two).Code != 400 {
		t.Fatal("two entries accepted")
	}
	fn := `{"name":"Has code","graph":{"nodes":[{"id":"f","type":"function","code":"return msg;"}]}}`
	if call(api, "itest-ff", "operator", "POST", "/v1/flow-fragments", fn).Code != 403 {
		t.Fatal("operator saved a function node")
	}
	if call(api, "itest-ff", "admin", "POST", "/v1/flow-fragments", fn).Code != 201 {
		t.Fatal("admin could not save a function fragment")
	}
	w = call(api, "itest-ff", "viewer", "POST", "/v1/flow-fragments/"+c.ID+"/instantiate", `{"prefix":"a1","x":10,"y":10}`)
	var inst struct {
		Nodes []struct{ ID string }
		Entry string
		Exits []string
	}
	json.Unmarshal(w.Body.Bytes(), &inst)
	if w.Code != 200 || inst.Entry != "a1_r" || len(inst.Nodes) != 2 || len(inst.Exits) != 1 || inst.Exits[0] != "a1_c" {
		t.Fatalf("instantiate %d %s", w.Code, w.Body.String())
	}
	if call(api, "itest-ff2", "viewer", "POST", "/v1/flow-fragments/"+c.ID+"/instantiate", `{"prefix":"a1"}`).Code != 404 || call(api, "itest-ff2", "operator", "DELETE", "/v1/flow-fragments/"+c.ID, "").Code != 404 {
		t.Fatal("cross-tenant access")
	}
	if call(api, "itest-ff", "operator", "DELETE", "/v1/flow-fragments/"+c.ID, "").Code != 204 {
		t.Fatal("delete")
	}
}
