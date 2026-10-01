package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
)

func TestIntegrationManageManyFlows(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-mf1")
	seed(t, s, "itest-mf2")
	ctx := t.Context()
	for _, tn := range []string{"itest-mf1", "itest-mf2"} {
		s.st.Pool.Exec(ctx, `UPDATE flows SET published_version_id=NULL WHERE tenant_id=$1`, tn)
		s.st.Pool.Exec(ctx, `DELETE FROM flows WHERE tenant_id=$1`, tn)
		s.st.Pool.Exec(ctx, `DELETE FROM tenant_features WHERE tenant_id=$1`, tn)
		s.st.Pool.Exec(ctx, `INSERT INTO notification_channels(id,tenant_id,type,target) VALUES($1,$2,'slack','C') ON CONFLICT DO NOTHING`, tn+"-ch", tn)
	}
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/flows", s.listFlows)
	api.HandleFunc("POST /v1/flows", s.createFlow)
	api.HandleFunc("POST /v1/flows/{id}/draft", s.createFlowDraft)
	api.HandleFunc("POST /v1/flows/{id}/publish", s.publishFlow)
	api.HandleFunc("GET /v1/flows/{id}/versions/{version}", s.getFlowVersion)
	api.HandleFunc("PATCH /v1/flows/{id}", s.patchFlow)
	api.HandleFunc("DELETE /v1/flows/{id}", s.deleteFlow)
	api.HandleFunc("POST /v1/flows/{id}/duplicate", s.duplicateFlow)
	api.HandleFunc("PUT /v1/features/{feature}", s.putFeature)

	mk := func(tn, name string) string {
		w := call(api, tn, "operator", "POST", "/v1/flows", strings.Replace(graphBody(tn, false), `"name":"graph"`, `"name":"`+name+`"`, 1))
		if w.Code != 201 {
			t.Fatalf("create %s: %d %s", name, w.Code, w.Body.String())
		}
		return fw2id(t, w)
	}
	a, b := mk("itest-mf1", "alpha"), mk("itest-mf1", "beta")
	c := mk("itest-mf2", "other-tenant")

	// list shows many flows with latest version
	lw := call(api, "itest-mf1", "viewer", "GET", "/v1/flows", "")
	var list []map[string]any
	json.Unmarshal(lw.Body.Bytes(), &list)
	if len(list) != 2 || list[0]["latest_version"] == nil {
		t.Fatalf("list %s", lw.Body.String())
	}

	// a second version is a draft; opening it returns exactly what was saved
	dw := call(api, "itest-mf1", "operator", "POST", "/v1/flows/"+a+"/draft", strings.SplitN(strings.Replace(graphBody("itest-mf1", false), `"value":50`, `"value":77`, 1), `"name":"graph",`, 2)[1][:0]+`{"definition":`+strings.TrimSuffix(strings.SplitN(strings.Replace(graphBody("itest-mf1", false), `"value":50`, `"value":77`, 1), `"definition":`, 2)[1], "}")+"}")
	if dw.Code != 201 {
		t.Fatalf("draft %d %s", dw.Code, dw.Body.String())
	}
	vw := call(api, "itest-mf1", "viewer", "GET", "/v1/flows/"+a+"/versions/2", "")
	if vw.Code != 200 || !strings.Contains(vw.Body.String(), `"value":77`) || !strings.Contains(vw.Body.String(), `"draft"`) {
		t.Fatalf("version %d %s", vw.Code, vw.Body.String())
	}
	if call(api, "itest-mf2", "viewer", "GET", "/v1/flows/"+a+"/versions/1", "").Code != 404 {
		t.Fatal("other tenant read a version")
	}

	// rename and disable; viewers and other tenants cannot
	if call(api, "itest-mf1", "operator", "PATCH", "/v1/flows/"+a, `{"name":"alpha renamed","enabled":false}`).Code != 200 {
		t.Fatal("patch failed")
	}
	var nm string
	var en bool
	s.st.Pool.QueryRow(ctx, `SELECT name, enabled FROM flows WHERE id=$1`, a).Scan(&nm, &en)
	if nm != "alpha renamed" || en {
		t.Fatalf("patch not applied: %q %v", nm, en)
	}
	for _, bad := range []string{`{"name":"<b>"}`, `{}`, `{"name":""}`} {
		if call(api, "itest-mf1", "operator", "PATCH", "/v1/flows/"+a, bad).Code != 400 {
			t.Fatalf("accepted %s", bad)
		}
	}
	if call(api, "itest-mf1", "viewer", "PATCH", "/v1/flows/"+a, `{"enabled":true}`).Code != 403 {
		t.Fatal("viewer patched")
	}
	if call(api, "itest-mf2", "operator", "PATCH", "/v1/flows/"+a, `{"enabled":true}`).Code != 404 {
		t.Fatal("other tenant patched")
	}
	// a disabled flow does not run; an enabled one does
	flow.Evaluate(ctx, s.st.Pool, nopNotifier{}, "itest-mf1", "itest-mf1-dev", "temp", 80)
	var runsA, runsB int
	s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM flow_runs WHERE flow_id=$1`, a).Scan(&runsA)
	s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM flow_runs WHERE flow_id=$1`, b).Scan(&runsB)
	if runsA != 0 || runsB != 1 {
		t.Fatalf("disabled flow ran or enabled did not: a=%d b=%d", runsA, runsB)
	}

	// duplicate lands as an unpublished draft with the newest content
	dp := call(api, "itest-mf1", "operator", "POST", "/v1/flows/"+a+"/duplicate", `{}`)
	if dp.Code != 201 {
		t.Fatalf("duplicate %d %s", dp.Code, dp.Body.String())
	}
	var pub *string
	var dname, dval string
	s.st.Pool.QueryRow(ctx, `SELECT published_version_id, name FROM flows WHERE id=$1`, fw2id(t, dp)).Scan(&pub, &dname)
	s.st.Pool.QueryRow(ctx, `SELECT definition->'graph'->'nodes'->0->>'value' FROM flow_versions WHERE flow_id=$1`, fw2id(t, dp)).Scan(&dval)
	if pub != nil || dname != "alpha renamed (copy)" || dval != "77" {
		t.Fatalf("duplicate wrong: pub=%v name=%q val=%q", pub, dname, dval)
	}
	if call(api, "itest-mf2", "operator", "POST", "/v1/flows/"+a+"/duplicate", `{}`).Code != 404 {
		t.Fatal("other tenant duplicated")
	}

	// delete: admin only, tenant scoped, cascades
	if call(api, "itest-mf1", "operator", "DELETE", "/v1/flows/"+b, "").Code != 403 {
		t.Fatal("operator deleted")
	}
	if call(api, "itest-mf2", "admin", "DELETE", "/v1/flows/"+b, "").Code != 404 {
		t.Fatal("other tenant deleted")
	}
	if call(api, "itest-mf1", "admin", "DELETE", "/v1/flows/"+b, "").Code != 200 {
		t.Fatal("admin delete failed")
	}
	var n int
	s.st.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM flows WHERE id=$1)+(SELECT count(*) FROM flow_versions WHERE flow_id=$1)+(SELECT count(*) FROM flow_runs WHERE flow_id=$1)`, b).Scan(&n)
	if n != 0 {
		t.Fatalf("delete left %d rows", n)
	}
	s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM flows WHERE id=$1`, c).Scan(&n)
	if n != 1 {
		t.Fatal("other tenant's flow affected")
	}

	// publishing a version with a function node needs admin + feature
	call(api, "itest-mf1", "admin", "PUT", "/v1/features/function_nodes", `{"enabled":true}`)
	fw := call(api, "itest-mf1", "admin", "POST", "/v1/flows?draft=1", graphBody("itest-mf1", true))
	if fw.Code != 201 {
		t.Fatalf("fn draft %d %s", fw.Code, fw.Body.String())
	}
	if call(api, "itest-mf1", "operator", "POST", "/v1/flows/"+fw2id(t, fw)+"/publish", `{"version":1}`).Code != 403 {
		t.Fatal("operator published a function-node flow")
	}
	if call(api, "itest-mf1", "admin", "POST", "/v1/flows/"+fw2id(t, fw)+"/publish", `{"version":1}`).Code != 200 {
		t.Fatal("admin publish failed")
	}
	s.st.Pool.Exec(ctx, `DELETE FROM tenant_features WHERE tenant_id='itest-mf1'`)
}
