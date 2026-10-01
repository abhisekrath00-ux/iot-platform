package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestIntegrationKPIs(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-kp1")
	seed(t, s, "itest-kp2")
	ctx := t.Context()
	s.st.Pool.Exec(ctx, `DELETE FROM kpis WHERE tenant_id IN ('itest-kp1','itest-kp2')`)
	t.Cleanup(func() { s.st.Pool.Exec(ctx, `DELETE FROM kpis WHERE tenant_id IN ('itest-kp1','itest-kp2')`) })
	// latest seeded temp is 21 (1 minute ago)
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/kpis", s.listKPIs)
	api.HandleFunc("POST /v1/kpis", s.createKPI)
	api.HandleFunc("DELETE /v1/kpis/{id}", s.deleteKPI)
	post := func(tenant, role, body string) (int, string) {
		w := call(api, tenant, role, "POST", "/v1/kpis", body)
		var o map[string]any
		json.Unmarshal(w.Body.Bytes(), &o)
		id, _ := o["id"].(string)
		return w.Code, id
	}
	code, id := post("itest-kp1", "operator", `{"name":"Temp in F","expression":"{itest-kp1-dev.temp} * 9 / 5 + 32","unit":"F"}`)
	if code != 201 {
		t.Fatalf("create %d", code)
	}
	for _, bad := range []string{
		`{"name":"x","expression":"{itest-kp2-dev.temp}"}`,     // other tenant's device
		`{"name":"x","expression":"{itest-kp1-dev.nope}"}`,     // unknown point
		`{"name":"x","expression":"1 + 2"}`,                    // no references
		`{"name":"x","expression":"{itest-kp1-dev.temp} ; 1"}`, // syntax
		`{"name":"<b>","expression":"{itest-kp1-dev.temp}"}`,   // name
	} {
		if c, _ := post("itest-kp1", "operator", bad); c != 400 {
			t.Fatalf("%s -> %d", bad, c)
		}
	}
	if c, _ := post("itest-kp1", "viewer", `{"name":"x","expression":"{itest-kp1-dev.temp}"}`); c != 403 {
		t.Fatalf("viewer %d", c)
	}
	post("itest-kp1", "operator", `{"name":"Div","expression":"1 / ({itest-kp1-dev.temp} - {itest-kp1-dev.temp})"}`)
	w := call(api, "itest-kp1", "viewer", "GET", "/v1/kpis", "")
	var list []map[string]any
	json.Unmarshal(w.Body.Bytes(), &list)
	got := map[string]map[string]any{}
	for _, k := range list {
		got[k["name"].(string)] = k
	}
	if v, _ := got["Temp in F"]["value"].(float64); v < 69.7 || v > 69.9 { // 21 C = 69.8 F
		t.Fatalf("Temp in F = %v (%v)", got["Temp in F"]["value"], got["Temp in F"])
	}
	if got["Div"]["value"] != nil || got["Div"]["error"] == nil {
		t.Fatalf("divide by zero must give an error and no value: %v", got["Div"])
	}
	// tenant isolation on list and delete
	w = call(api, "itest-kp2", "viewer", "GET", "/v1/kpis", "")
	if w.Body.String() != "[]\n" {
		t.Fatalf("other tenant sees KPIs: %s", w.Body.String())
	}
	if call(api, "itest-kp2", "operator", "DELETE", "/v1/kpis/"+id, "").Code != 404 {
		t.Fatal("cross-tenant delete")
	}
	if call(api, "itest-kp1", "operator", "DELETE", "/v1/kpis/"+id, "").Code != 200 {
		t.Fatal("delete")
	}
}
