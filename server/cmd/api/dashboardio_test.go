package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestIntegrationDashboardIO(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-di")
	seed(t, s, "itest-di2")
	ctx := t.Context()
	s.st.Pool.Exec(ctx, `DELETE FROM dashboards WHERE tenant_id IN ('itest-di','itest-di2')`)
	t.Cleanup(func() { s.st.Pool.Exec(ctx, `DELETE FROM dashboards WHERE tenant_id IN ('itest-di','itest-di2')`) })
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/dashboards", s.saveDashboard)
	mux.HandleFunc("GET /v1/dashboards/{id}/export", s.exportDashboard)
	mux.HandleFunc("POST /v1/dashboards/import", s.importDashboard)

	w := call(mux, "itest-di", "operator", "POST", "/v1/dashboards", `{"name":"Plant","layout":{"widgets":[{"id":"w1","type":"gauge","device_id":"itest-di-dev","point_id":"temp"},{"id":"w2","type":"gauge","device_id":"elsewhere-dev","point_id":"x"}]}}`)
	var c struct{ ID string }
	json.Unmarshal(w.Body.Bytes(), &c)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	ex := call(mux, "itest-di", "viewer", "GET", "/v1/dashboards/"+c.ID+"/export", "")
	if ex.Code != 200 || !strings.Contains(ex.Body.String(), "hexmon-dashboard/1") {
		t.Fatalf("export: %d %s", ex.Code, ex.Body.String())
	}
	if call(mux, "itest-di2", "admin", "GET", "/v1/dashboards/"+c.ID+"/export", "").Code != 404 {
		t.Fatal("another tenant must not export it")
	}
	if call(mux, "itest-di2", "viewer", "POST", "/v1/dashboards/import", ex.Body.String()).Code != 403 {
		t.Fatal("viewer import")
	}
	// import into the other tenant: both devices are unknown there
	im := call(mux, "itest-di2", "admin", "POST", "/v1/dashboards/import", ex.Body.String())
	var r struct {
		ID      string
		Widgets int
		Missing []string `json:"missing_devices"`
	}
	json.Unmarshal(im.Body.Bytes(), &r)
	if im.Code != 201 || r.Widgets != 2 || len(r.Missing) != 2 {
		t.Fatalf("import: %d %s", im.Code, im.Body.String())
	}
	// back into the source tenant: one known device, one missing
	im = call(mux, "itest-di", "admin", "POST", "/v1/dashboards/import", ex.Body.String())
	json.Unmarshal(im.Body.Bytes(), &r)
	if im.Code != 201 || len(r.Missing) != 1 || r.Missing[0] != "elsewhere-dev" {
		t.Fatalf("re-import: %d %s", im.Code, im.Body.String())
	}
	for _, bad := range []string{`{"format":"other","name":"x","layout":{}}`, `{"format":"hexmon-dashboard/1","name":"","layout":{}}`, `{"format":"hexmon-dashboard/1","name":"x","layout":"nope"}`} {
		if call(mux, "itest-di", "admin", "POST", "/v1/dashboards/import", bad).Code != 400 {
			t.Fatalf("accepted %s", bad)
		}
	}
}
