package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestIntegrationLiveSubflows(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-sf1")
	seed(t, s, "itest-sf2")
	ctx := t.Context()
	pool := s.st.Pool
	clean := func() {
		pool.Exec(ctx, `UPDATE flows SET published_version_id=NULL WHERE tenant_id IN ('itest-sf1','itest-sf2')`)
		pool.Exec(ctx, `DELETE FROM flow_versions WHERE tenant_id IN ('itest-sf1','itest-sf2')`)
		pool.Exec(ctx, `DELETE FROM flows WHERE tenant_id IN ('itest-sf1','itest-sf2')`)
		pool.Exec(ctx, `DELETE FROM flow_fragments WHERE tenant_id IN ('itest-sf1','itest-sf2')`)
		pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id IN ('itest-sf1','itest-sf2')`)
	}
	pool.Exec(ctx, `INSERT INTO notification_channels(id,tenant_id,type,target) VALUES('itest-sf1-ch','itest-sf1','email','ops@example.com') ON CONFLICT DO NOTHING`)
	clean()
	t.Cleanup(clean)
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/flow-fragments", s.createFragment)
	api.HandleFunc("PUT /v1/flow-fragments/{id}", s.updateFragment)
	api.HandleFunc("GET /v1/flow-fragments/{id}/usage", s.fragmentUsage)
	api.HandleFunc("DELETE /v1/flow-fragments/{id}", s.deleteFragment)
	api.HandleFunc("POST /v1/flows", s.createFlow)
	api.HandleFunc("POST /v1/flows/{id}/draft", s.createFlowDraft)
	api.HandleFunc("POST /v1/flows/{id}/publish", s.publishFlow)
	api.HandleFunc("POST /v1/flows/{id}/refresh-subflows", s.refreshSubflows)
	frag := func(thr string) string {
		return `{"nodes":[{"id":"r","type":"range","in_min":0,"in_max":100,"out_min":0,"out_max":1,"clamp":true},{"id":"c","type":"condition","op":">","value":` + thr + `}],"edges":[{"from":"r","to":"c"}]}`
	}
	w := call(api, "itest-sf1", "operator", "POST", "/v1/flow-fragments", `{"name":"Scale","graph":`+frag("0.5")+`}`)
	var fr struct{ ID string }
	json.Unmarshal(w.Body.Bytes(), &fr)
	if w.Code != 201 {
		t.Fatalf("fragment: %d %s", w.Code, w.Body)
	}
	flowDef := func(fid string, extra string) string {
		return `{"name":"Uses scale","definition":{"graph":{"nodes":[{"id":"t","type":"trigger","device_id":"itest-sf1-dev","point_id":"temp","op":">","value":1},{"id":"sf","type":"subflow","fragment_id":"` + fid + `"},{"id":"d","type":"notify","channel_id":"itest-sf1-ch"}],"edges":[{"from":"t","to":"sf"},{"from":"sf","to":"d"}]}` + extra + `}}`
	}
	w = call(api, "itest-sf1", "operator", "POST", "/v1/flows", flowDef(fr.ID, `,"source":{"nodes":[{"id":"evil","type":"debug"}],"edges":[]}`))
	if w.Code >= 300 {
		t.Fatalf("create flow: %d %s", w.Code, w.Body)
	}
	var fl struct{ ID string }
	json.Unmarshal(w.Body.Bytes(), &fl)
	var raw string
	get := func(ver int) string {
		if err := pool.QueryRow(ctx, `SELECT definition::text FROM flow_versions WHERE flow_id=$1 AND version=$2`, fl.ID, ver).Scan(&raw); err != nil {
			t.Fatalf("read version %d: %v", ver, err)
		}
		return raw
	}
	d1 := get(1)
	for _, want := range []string{`"id": "sf_r"`, `"id": "sf_c"`, `"fragment_version": 1`, `"from": "sf_c"`, `"to": "sf_r"`} {
		if !strings.Contains(d1, want) {
			t.Fatalf("v1 lacks %s: %s", want, d1)
		}
	}
	if strings.Contains(d1, "evil") {
		t.Fatal("client-supplied source kept")
	}
	// edit the fragment: v2. The flow stays on v1 until a new draft is made and published.
	if w := call(api, "itest-sf1", "operator", "PUT", "/v1/flow-fragments/"+fr.ID, `{"graph":`+frag("0.9")+`}`); w.Code != 200 {
		t.Fatalf("update fragment: %d %s", w.Code, w.Body)
	}
	if get(1) != d1 {
		t.Fatal("editing a fragment changed a stored flow version")
	}
	w = call(api, "itest-sf1", "viewer", "GET", "/v1/flow-fragments/"+fr.ID+"/usage", "")
	if !strings.Contains(w.Body.String(), `"out_of_date":true`) || !strings.Contains(w.Body.String(), `"current_version":2`) {
		t.Fatalf("usage: %s", w.Body)
	}
	if w := call(api, "itest-sf1", "operator", "POST", "/v1/flows/"+fl.ID+"/refresh-subflows", ``); w.Code != 201 {
		t.Fatalf("refresh: %d %s", w.Code, w.Body)
	}
	d2 := get(2)
	if !strings.Contains(d2, `"fragment_version": 2`) || !strings.Contains(d2, `"value": 0.9`) {
		t.Fatalf("v2 not refreshed: %s", d2)
	}
	var status string
	pool.QueryRow(ctx, `SELECT status FROM flow_versions WHERE flow_id=$1 AND version=2`, fl.ID).Scan(&status)
	if status != "draft" {
		t.Fatalf("refresh published the new version (%s)", status)
	}
	if w := call(api, "itest-sf1", "operator", "DELETE", "/v1/flow-fragments/"+fr.ID, ""); w.Code != 409 {
		t.Fatalf("deleted a fragment in use: %d", w.Code)
	}
	// rejections
	if w := call(api, "itest-sf2", "operator", "POST", "/v1/flows", flowDef(fr.ID, ``)); w.Code != 400 || !strings.Contains(w.Body.String(), "fragment not found") {
		t.Fatalf("cross-tenant fragment: %d %s", w.Code, w.Body)
	}
	nested := `{"name":"Nested","graph":{"nodes":[{"id":"s","type":"subflow","fragment_id":"` + fr.ID + `"}]}}`
	if w := call(api, "itest-sf1", "operator", "POST", "/v1/flow-fragments", nested); w.Code != 400 {
		t.Fatalf("fragment containing a subflow: %d", w.Code)
	}
	if w := call(api, "itest-sf1", "operator", "POST", "/v1/flows", strings.Replace(flowDef(fr.ID, ``), `"fragment_id":"`+fr.ID+`"`, `"fragment_id":"`+fr.ID+`","fragment_version":9`, 1)); w.Code != 400 {
		t.Fatalf("unknown pinned version: %d", w.Code)
	}
}
