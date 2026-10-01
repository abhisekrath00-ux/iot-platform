package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestIntegrationFlowExportImportRoundTrip(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-fx1")
	seed(t, s, "itest-fx2")
	ctx := t.Context()
	for _, tn := range []string{"itest-fx1", "itest-fx2"} {
		s.st.Pool.Exec(ctx, `DELETE FROM flows WHERE tenant_id=$1`, tn)
		s.st.Pool.Exec(ctx, `DELETE FROM flow_versions WHERE tenant_id=$1`, tn)
	}
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/flows", s.createFlow)
	api.HandleFunc("GET /v1/flows/{id}/export", s.exportFlow)
	api.HandleFunc("POST /v1/flows/import", s.importFlow)
	for _, tn := range []string{"itest-fx1", "itest-fx2"} {
		if _, err := s.st.Pool.Exec(ctx, `INSERT INTO notification_channels(id,tenant_id,type,target) VALUES($1,$2,'slack','C-OPS') ON CONFLICT DO NOTHING`, tn+"-ch", tn); err != nil {
			t.Fatal(err)
		}
	}
	w := call(api, "itest-fx1", "operator", "POST", "/v1/flows", `{"name":"boiler","definition":{"trigger":{"device_id":"itest-fx1-dev","point_id":"temp","op":">","value":90},"steps":[{"type":"notify","channel_id":"itest-fx1-ch","message":"hot {value}"}]}}`)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var created map[string]any
	json.Unmarshal(w.Body.Bytes(), &created)
	ex := call(api, "itest-fx1", "viewer", "GET", "/v1/flows/"+created["id"].(string)+"/export", "")
	if ex.Code != 200 || ex.Header().Get("Content-Disposition") == "" {
		t.Fatalf("export %d %s", ex.Code, ex.Body.String())
	}
	var file map[string]any
	json.Unmarshal(ex.Body.Bytes(), &file)
	if file["channels"].(map[string]any)["0"] != "slack:C-OPS" {
		t.Fatalf("channel label missing: %s", ex.Body.String())
	}
	if call(api, "itest-fx2", "operator", "GET", "/v1/flows/"+created["id"].(string)+"/export", "").Code != 404 {
		t.Fatal("other tenant exported a flow")
	}
	// Import into the other tenant resolves its own channel and lands unpublished.
	im := call(api, "itest-fx2", "operator", "POST", "/v1/flows/import", ex.Body.String())
	if im.Code != 201 {
		t.Fatalf("import %d %s", im.Code, im.Body.String())
	}
	var pub *string
	s.st.Pool.QueryRow(ctx, `SELECT published_version_id FROM flows WHERE tenant_id='itest-fx2'`).Scan(&pub)
	if pub != nil {
		t.Fatal("imported flow must not be published")
	}
	var chID string
	s.st.Pool.QueryRow(ctx, `SELECT definition->'steps'->0->>'channel_id' FROM flow_versions WHERE tenant_id='itest-fx2'`).Scan(&chID)
	if chID != "itest-fx2-ch" {
		t.Fatalf("channel not remapped: %q", chID)
	}
	// Missing channel, wrong format and viewer role are refused.
	s.st.Pool.Exec(ctx, `DELETE FROM notification_channels WHERE tenant_id='itest-fx2'`)
	if call(api, "itest-fx2", "operator", "POST", "/v1/flows/import", ex.Body.String()).Code != 400 {
		t.Fatal("import without channel should fail")
	}
	if call(api, "itest-fx2", "operator", "POST", "/v1/flows/import", `{"format":"node-red","name":"x"}`).Code != 400 {
		t.Fatal("wrong format accepted")
	}
	if call(api, "itest-fx2", "viewer", "POST", "/v1/flows/import", ex.Body.String()).Code != 403 {
		t.Fatal("viewer imported")
	}
}
