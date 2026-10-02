package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
)

func graphBody(tenant string, fn bool) string {
	mid := `{"id":"ch","type":"change","changes":[{"action":"set","property":"vars.level","value":"high"}]}`
	if fn {
		mid = `{"id":"ch","type":"function","code":"msg.value = msg.value * 2; return msg;"}`
	}
	return `{"name":"graph","definition":{"graph":{"nodes":[
	  {"id":"t","type":"trigger","device_id":"` + tenant + `-dev","point_id":"temp","op":">","value":50},
	  ` + mid + `,
	  {"id":"dbg","type":"debug","message":"v={value} level={vars.level}"},
	  {"id":"n","type":"notify","channel_id":"` + tenant + `-ch","message":"{vars.level} {value}"}],
	 "edges":[{"from":"t","to":"ch"},{"from":"ch","to":"dbg"},{"from":"ch","to":"n"}]}}}`
}

func TestIntegrationGraphFlowsAndFunctionNodeGating(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-gf1")
	seed(t, s, "itest-gf2")
	ctx := t.Context()
	for _, tn := range []string{"itest-gf1", "itest-gf2"} {
		s.st.Pool.Exec(ctx, `DELETE FROM flows WHERE tenant_id=$1`, tn)
		s.st.Pool.Exec(ctx, `DELETE FROM flow_versions WHERE tenant_id=$1`, tn)
		s.st.Pool.Exec(ctx, `DELETE FROM tenant_features WHERE tenant_id=$1`, tn)
		s.st.Pool.Exec(ctx, `INSERT INTO notification_channels(id,tenant_id,type,target) VALUES($1,$2,'slack','C-OPS') ON CONFLICT DO NOTHING`, tn+"-ch", tn)
	}
	t.Cleanup(func() {
		s.st.Pool.Exec(ctx, `DELETE FROM tenant_features WHERE tenant_id IN ('itest-gf1','itest-gf2')`)
	})
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/flows", s.createFlow)
	api.HandleFunc("GET /v1/flows/{id}/export", s.exportFlow)
	api.HandleFunc("POST /v1/flows/import", s.importFlow)
	api.HandleFunc("POST /v1/flows/graph/test", s.testFlowGraph)
	api.HandleFunc("POST /v1/flows/convert", s.convertFlow)
	api.HandleFunc("GET /v1/flows/{id}/export-nodered", s.exportNodeRED)
	api.HandleFunc("POST /v1/flows/import-nodered", s.importNodeRED)
	api.HandleFunc("GET /v1/features", s.getFeatures)
	api.HandleFunc("PUT /v1/features/{feature}", s.putFeature)

	// plain graph flow: operator may save it; it runs end to end
	w := call(api, "itest-gf1", "operator", "POST", "/v1/flows", graphBody("itest-gf1", false))
	if w.Code != 201 {
		t.Fatalf("create graph %d %s", w.Code, w.Body.String())
	}
	var created map[string]any
	json.Unmarshal(w.Body.Bytes(), &created)
	flow.Evaluate(ctx, s.st.Pool, nopNotifier{}, "itest-gf1", "itest-gf1-dev", "temp", 80)
	var outcome, detail string
	if err := s.st.Pool.QueryRow(ctx, `SELECT outcome, COALESCE(detail,'') FROM flow_runs WHERE flow_id=$1 ORDER BY created_at DESC LIMIT 1`, created["id"]).Scan(&outcome, &detail); err != nil {
		t.Fatal(err)
	}
	if outcome != "notified" || !strings.Contains(detail, "v=80 level=high") {
		t.Fatalf("run %q detail %q", outcome, detail)
	}

	// ?draft=1 saves without publishing
	dw := call(api, "itest-gf1", "operator", "POST", "/v1/flows?draft=1", graphBody("itest-gf1", false))
	if dw.Code != 201 {
		t.Fatalf("draft create %d %s", dw.Code, dw.Body.String())
	}
	var pubd *string
	s.st.Pool.QueryRow(ctx, `SELECT published_version_id FROM flows WHERE id=$1`, fw2id(t, dw)).Scan(&pubd)
	if pubd != nil {
		t.Fatal("?draft=1 must not publish")
	}

	// export carries the node-index channel label; import into another tenant remaps it as a draft
	ex := call(api, "itest-gf1", "viewer", "GET", "/v1/flows/"+created["id"].(string)+"/export", "")
	if ex.Code != 200 || !strings.Contains(ex.Body.String(), `"3":"slack:C-OPS"`) || strings.Contains(ex.Body.String(), "itest-gf1-ch") {
		t.Fatalf("export %d %s", ex.Code, ex.Body.String())
	}
	im := call(api, "itest-gf2", "operator", "POST", "/v1/flows/import", ex.Body.String())
	if im.Code != 201 {
		t.Fatalf("import %d %s", im.Code, im.Body.String())
	}
	var got string
	s.st.Pool.QueryRow(ctx, `SELECT definition->'graph'->'nodes'->3->>'channel_id' FROM flow_versions WHERE tenant_id='itest-gf2'`).Scan(&got)
	if got != "itest-gf2-ch" {
		t.Fatalf("channel not remapped: %q", got)
	}

	// function nodes: refused until an admin enables the feature, and never for operators
	if c := call(api, "itest-gf1", "admin", "POST", "/v1/flows", graphBody("itest-gf1", true)).Code; c != 403 {
		t.Fatalf("function node accepted while feature off: %d", c)
	}
	if c := call(api, "itest-gf1", "operator", "PUT", "/v1/features/function_nodes", `{"enabled":true}`).Code; c != 403 {
		t.Fatalf("operator toggled feature: %d", c)
	}
	if c := call(api, "itest-gf1", "admin", "PUT", "/v1/features/function_nodes", `{"enabled":true}`).Code; c != 200 {
		t.Fatalf("enable: %d", c)
	}
	if c := call(api, "itest-gf1", "operator", "POST", "/v1/flows", graphBody("itest-gf1", true)).Code; c != 403 {
		t.Fatalf("operator saved a function node: %d", c)
	}
	if c := call(api, "itest-gf2", "admin", "POST", "/v1/flows", graphBody("itest-gf2", true)).Code; c != 403 {
		t.Fatalf("other tenant not enabled but accepted: %d", c)
	}
	bad := strings.Replace(graphBody("itest-gf1", true), "return msg;", "return (", 1)
	if c := call(api, "itest-gf1", "admin", "POST", "/v1/flows", bad).Code; c != 400 {
		t.Fatalf("syntax error accepted: %d", c)
	}
	fw := call(api, "itest-gf1", "admin", "POST", "/v1/flows", graphBody("itest-gf1", true))
	if fw.Code != 201 {
		t.Fatalf("admin function flow %d %s", fw.Code, fw.Body.String())
	}

	// dry-run: function runs for the admin, is disabled for the other tenant, no run recorded
	tb := `{"value":80,"definition":` + strings.TrimSuffix(strings.SplitN(graphBody("itest-gf1", true), `"definition":`, 2)[1], "}") + "}"
	tr := call(api, "itest-gf1", "admin", "POST", "/v1/flows/graph/test", tb)
	if tr.Code != 200 || !strings.Contains(tr.Body.String(), `v=160 level={vars.level}`) {
		t.Fatalf("test %d %s", tr.Code, tr.Body.String())
	}
	tr = call(api, "itest-gf2", "operator", "POST", "/v1/flows/graph/test", strings.ReplaceAll(tb, "itest-gf1", "itest-gf2"))
	if tr.Code != 200 || !strings.Contains(tr.Body.String(), "disabled") {
		t.Fatalf("test (feature off) %d %s", tr.Code, tr.Body.String())
	}

	// published function flow executes only while the tenant keeps the feature on
	flow.EvaluateWith(ctx, s.st.Pool, nopNotifier{}, fnRunner, "itest-gf1", "itest-gf1-dev", "temp", 70)
	var d1 string
	s.st.Pool.QueryRow(ctx, `SELECT COALESCE(detail,'') FROM flow_runs WHERE flow_id=$1 ORDER BY created_at DESC LIMIT 1`, fw2id(t, fw)).Scan(&d1)
	if !strings.Contains(d1, "v=140") {
		t.Fatalf("function did not run: %q", d1)
	}
	call(api, "itest-gf1", "admin", "PUT", "/v1/features/function_nodes", `{"enabled":false}`)
	time.Sleep(5 * time.Millisecond)
	flow.EvaluateWith(ctx, s.st.Pool, nopNotifier{}, fnRunner, "itest-gf1", "itest-gf1-dev", "temp", 71)
	s.st.Pool.QueryRow(ctx, `SELECT COALESCE(detail,'') FROM flow_runs WHERE flow_id=$1 ORDER BY created_at DESC LIMIT 1`, fw2id(t, fw)).Scan(&d1)
	if !strings.Contains(d1, "disabled") && d1 != "" {
		t.Fatalf("function ran after disable: %q", d1)
	}
	if strings.Contains(d1, "v=142") {
		t.Fatal("function ran after disable")
	}

	// Node-RED interchange: export the first (plain graph) flow, import it elsewhere as a draft
	nr := call(api, "itest-gf1", "viewer", "GET", "/v1/flows/"+created["id"].(string)+"/export-nodered", "")
	if nr.Code != 200 || !strings.Contains(nr.Body.String(), `"channel_id":"slack:C-OPS"`) || !strings.Contains(nr.Body.String(), `"type":"change"`) {
		t.Fatalf("nodered export %d %s", nr.Code, nr.Body.String())
	}
	imp := call(api, "itest-gf2", "operator", "POST", "/v1/flows/import-nodered", `{"name":"from node-red","flows":`+nr.Body.String()+`}`)
	if imp.Code != 201 {
		t.Fatalf("nodered import %d %s", imp.Code, imp.Body.String())
	}
	unsup := call(api, "itest-gf2", "operator", "POST", "/v1/flows/import-nodered", `{"name":"x","flows":[{"id":"a","type":"http request"}]}`)
	if unsup.Code != 400 || !strings.Contains(unsup.Body.String(), "http request") {
		t.Fatalf("unsupported node not refused: %d %s", unsup.Code, unsup.Body.String())
	}
	if call(api, "itest-gf2", "viewer", "POST", "/v1/flows/import-nodered", `{"name":"x","flows":[]}`).Code != 403 {
		t.Fatal("viewer imported")
	}

	// legacy -> graph conversion is stateless and validates
	cv := call(api, "itest-gf1", "viewer", "POST", "/v1/flows/convert", `{"definition":{"trigger":{"device_id":"d","point_id":"p","op":">","value":1},"steps":[{"type":"delay","seconds":5},{"type":"notify","channel_id":"c"}]}}`)
	if cv.Code != 200 || !strings.Contains(cv.Body.String(), `"type":"delay"`) {
		t.Fatalf("convert %d %s", cv.Code, cv.Body.String())
	}
}

func fw2id(t *testing.T, w *httptest.ResponseRecorder) string {
	var m map[string]any
	json.Unmarshal(w.Body.Bytes(), &m)
	return m["id"].(string)
}

func TestIntegrationHTTPNodeGate(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-hn1")
	t.Cleanup(func() { s.st.Pool.Exec(t.Context(), `DELETE FROM tenant_features WHERE tenant_id='itest-hn1'`) })
	api := http.NewServeMux()
	api.HandleFunc("PUT /v1/features/{feature}", s.putFeature)
	api.HandleFunc("GET /v1/features", s.getFeatures)
	api.HandleFunc("POST /v1/flows/graph/test", s.testFlowGraph)
	def := `{"definition":{"graph":{"nodes":[{"id":"t","type":"trigger","device_id":"d","point_id":"p","op":">","value":1},
	  {"id":"h","type":"http","method":"GET","url":"http://10.0.0.7/x","target":"r"},
	  {"id":"n","type":"notify","channel_id":"c","message":"ok {vars.r}"},{"id":"f","type":"notify","channel_id":"c","message":"failed"}],
	  "edges":[{"from":"t","to":"h"},{"from":"h","port":"0","to":"n"},{"from":"h","port":"1","to":"f"}]}},"value":5}`
	// A dry run never sends the request: it takes the failure port and says why.
	w := call(api, "itest-hn1", "operator", "POST", "/v1/flows/graph/test", def)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "failed") || !strings.Contains(w.Body.String(), "dry run") {
		t.Fatalf("test run: %d %s", w.Code, w.Body.String())
	}
	if c := call(api, "itest-hn1", "operator", "PUT", "/v1/features/http_nodes", `{"enabled":true}`).Code; c != 403 {
		t.Errorf("operator enabled http nodes: %d", c)
	}
	if c := call(api, "itest-hn1", "admin", "PUT", "/v1/features/http_nodes", `{"enabled":true}`).Code; c != 200 {
		t.Errorf("admin enable: %d", c)
	}
	if w := call(api, "itest-hn1", "viewer", "GET", "/v1/features", ""); !strings.Contains(w.Body.String(), `"http_nodes":true`) {
		t.Errorf("features: %s", w.Body.String())
	}
	if c := call(api, "itest-hn1", "admin", "PUT", "/v1/features/other_thing", `{"enabled":true}`).Code; c != 404 {
		t.Errorf("unknown feature: %d", c)
	}
}
