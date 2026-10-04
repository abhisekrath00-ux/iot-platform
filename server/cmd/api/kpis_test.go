package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
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

func TestIntegrationKPIHistory(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-kh")
	seed(t, s, "itest-kh2")
	ctx := t.Context()
	clean := func() {
		s.st.Pool.Exec(ctx, `DELETE FROM kpis WHERE tenant_id IN ('itest-kh','itest-kh2')`)
		s.st.Pool.Exec(ctx, `DELETE FROM telemetry_rollup_hourly WHERE tenant_id='itest-kh'`)
	}
	clean()
	t.Cleanup(clean)
	// an old hour that exists only as a rollup (raw already purged): avg 40, plus the seeded recent raw readings
	s.st.Pool.Exec(ctx, `INSERT INTO telemetry_rollup_hourly(tenant_id,device_id,point_id,bucket,n,sum,min,max) VALUES('itest-kh','itest-kh-dev','temp',date_trunc('hour', now()) - interval '5 hours',4,160,30,50)`)
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/kpis", s.createKPI)
	api.HandleFunc("GET /v1/kpis/{id}/history", s.kpiHistory)
	w := call(api, "itest-kh", "operator", "POST", "/v1/kpis", `{"name":"Temp x2","expression":"{itest-kh-dev.temp} * 2","unit":"C"}`)
	var c struct{ ID string }
	json.Unmarshal(w.Body.Bytes(), &c)
	h := call(api, "itest-kh", "viewer", "GET", "/v1/kpis/"+c.ID+"/history?hours=12", "")
	var o struct {
		Points []struct {
			T time.Time
			V float64
		}
		Min, Max float64
	}
	json.Unmarshal(h.Body.Bytes(), &o)
	if h.Code != 200 || len(o.Points) < 2 {
		t.Fatalf("history: %d %s", h.Code, h.Body.String())
	}
	if o.Points[0].V != 80 { // the rollup-only hour: 160/4 = 40, doubled
		t.Fatalf("the rollup hour should read 80, got %+v", o.Points)
	}
	for i := 1; i < len(o.Points); i++ {
		if !o.Points[i].T.After(o.Points[i-1].T) {
			t.Fatal("points must be in time order")
		}
	}
	// min and max must match the returned points (the recent raw hours average differently depending on the
	// minute the test runs, so their exact values are not pinned)
	lo, hi := o.Points[0].V, o.Points[0].V
	for _, p := range o.Points {
		lo, hi = min(lo, p.V), max(hi, p.V)
	}
	if o.Min != lo || o.Max != hi || o.Max != 80 {
		t.Fatalf("min/max: %+v", o)
	}
	if call(api, "itest-kh2", "viewer", "GET", "/v1/kpis/"+c.ID+"/history", "").Code != 404 {
		t.Fatal("another tenant read it")
	}
	if call(api, "itest-kh", "viewer", "GET", "/v1/kpis/"+c.ID+"/history?hours=9999", "").Code != 400 {
		t.Fatal("hours cap")
	}
}
