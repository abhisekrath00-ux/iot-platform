package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestIntegrationAssetFiles(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-af")
	seed(t, s, "itest-af2")
	ctx := t.Context()
	clean := func() {
		s.st.Pool.Exec(ctx, `DELETE FROM asset_files WHERE tenant_id IN ('itest-af','itest-af2')`)
		s.st.Pool.Exec(ctx, `DELETE FROM assets WHERE tenant_id IN ('itest-af','itest-af2')`)
		s.st.Pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id IN ('itest-af','itest-af2')`)
	}
	clean()
	t.Cleanup(clean)
	s.st.Pool.Exec(ctx, `INSERT INTO assets(id,tenant_id,name) VALUES('itest-af-a','itest-af','Pump')`)
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/assets/{id}/files", s.uploadAssetFile)
	api.HandleFunc("GET /v1/assets/{id}/files", s.listAssetFiles)
	api.HandleFunc("GET /v1/assets/{id}/files/{fid}", s.downloadAssetFile)
	api.HandleFunc("DELETE /v1/assets/{id}/files/{fid}", s.deleteAssetFile)
	base := "/v1/assets/itest-af-a/files"
	w := call(api, "itest-af", "operator", "POST", base+"?name=manual.pdf", "%PDF-1.4 hello")
	if w.Code != 201 {
		t.Fatalf("upload %d %s", w.Code, w.Body.String())
	}
	var up struct{ ID string }
	json.Unmarshal(w.Body.Bytes(), &up)
	for name, c := range map[string]struct{ q, body string }{
		"wrong content": {"?name=x.pdf", "<script>alert(1)</script>"},
		"html ext":      {"?name=x.html", "<b>"},
		"traversal":     {"?name=..%2Fx.pdf", "%PDF-1"},
		"empty":         {"?name=x.txt", ""},
	} {
		if got := call(api, "itest-af", "operator", "POST", base+c.q, c.body).Code; got != 400 {
			t.Fatalf("%s -> %d", name, got)
		}
	}
	if call(api, "itest-af", "viewer", "POST", base+"?name=y.txt", "hi").Code != 403 {
		t.Fatal("viewer uploaded")
	}
	if call(api, "itest-af2", "operator", "POST", base+"?name=y.txt", "hi").Code != 404 {
		t.Fatal("cross-tenant upload")
	}
	big := make([]byte, assetFileMax+10)
	copy(big, "%PDF-")
	if call(api, "itest-af", "operator", "POST", base+"?name=big.pdf", string(big)).Code != 413 {
		t.Fatal("size cap")
	}
	d := call(api, "itest-af", "viewer", "GET", base+"/"+up.ID, "")
	if d.Code != 200 || d.Body.String() != "%PDF-1.4 hello" || d.Header().Get("X-Content-Type-Options") != "nosniff" || d.Header().Get("Content-Disposition")[:10] != "attachment" {
		t.Fatalf("download %d %v", d.Code, d.Header())
	}
	if call(api, "itest-af2", "viewer", "GET", base+"/"+up.ID, "").Code != 404 {
		t.Fatal("cross-tenant download")
	}
	if call(api, "itest-af", "operator", "DELETE", base+"/"+up.ID, "").Code != 204 || call(api, "itest-af", "viewer", "GET", base+"/"+up.ID, "").Code != 404 {
		t.Fatal("delete")
	}
}
