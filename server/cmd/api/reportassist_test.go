package main

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestIntegrationAssistantReportFormulas(t *testing.T) {
	s, h := testServer(t)
	seed(t, s, "itest-ra")
	check := func(kind, expr string) map[string]any {
		w := call(h, "itest-ra", "admin", "GET", "/v1/reports/check-formula?kind="+kind+"&expression="+url.QueryEscape(expr), "")
		if w.Code != 200 {
			t.Fatalf("%s %q: %d %s", kind, expr, w.Code, w.Body.String())
		}
		var m map[string]any
		json.Unmarshal(w.Body.Bytes(), &m)
		return m
	}
	for _, e := range []string{"if({row.avg} > 50 and {row.max} < 100, 1, 0)", "if(not({row.count} >= 10) or {row.min} < 5, 1, 0)"} {
		if m := check("highlight", e); m["ok"] != true {
			t.Errorf("%q should be valid: %v", e, m)
		}
	}
	for _, e := range []string{"if({row.avg} >, 1, 0)", "nosuch({row.avg})", "if({row.bogus} > 1, 1, 0)", "((1)"} {
		if m := check("highlight", e); m["ok"] == true || m["error"] == nil {
			t.Errorf("%q should be refused: %v", e, m)
		}
	}
	if m := check("kpi", "{row.avg} + 1"); m["ok"] == true {
		t.Errorf("row fields must be refused in a kpi: %v", m)
	}
	if m := check("kpi", "clamp({d1.p} * 2, 0, 100)"); m["ok"] != true {
		t.Errorf("kpi formula: %v", m)
	}
	// create_report path: valid rule saves, invalid rule is refused server side.
	ok := call(h, "itest-ra", "admin", "POST", "/v1/reports/simple", `{"name":"AI","device_id":"itest-ra-dev","point_id":"temp","highlight_when":"if({row.avg} > 50 and {row.max} < 100, 1, 0)","highlight_color":"red"}`)
	if ok.Code != 201 {
		t.Fatalf("valid simple report: %d %s", ok.Code, ok.Body.String())
	}
	bad := call(h, "itest-ra", "admin", "POST", "/v1/reports/simple", `{"name":"AI","device_id":"itest-ra-dev","point_id":"temp","highlight_when":"if({row.avg} >"}`)
	if bad.Code != 400 {
		t.Errorf("invalid rule must be 400, got %d", bad.Code)
	}
	if v := call(h, "itest-ra", "viewer", "POST", "/v1/reports/simple", `{"name":"AI","device_id":"itest-ra-dev","point_id":"temp"}`); v.Code != 403 {
		t.Errorf("viewer must be refused: %d %s", v.Code, strings.TrimSpace(v.Body.String()))
	}
}
