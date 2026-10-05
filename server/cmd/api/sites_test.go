package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestIntegrationCreateSite(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-site")
	ctx := t.Context()
	clean := func() {
		s.st.Pool.Exec(ctx, `DELETE FROM sites WHERE tenant_id='itest-site' AND id<>'itest-site-site'`)
		s.st.Pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id='itest-site'`)
	}
	clean()
	t.Cleanup(clean)
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/sites", s.listSites)
	api.HandleFunc("POST /v1/sites", s.createSite)
	if call(api, "itest-site", "operator", "POST", "/v1/sites", `{"name":"X"}`).Code != 403 {
		t.Fatal("operator created a site")
	}
	for _, bad := range []string{`{"name":""}`, `{"name":"<b>"}`, `{"name":"` + strings.Repeat("a", 81) + `"}`, `nope`} {
		if w := call(api, "itest-site", "admin", "POST", "/v1/sites", bad); w.Code != 400 {
			t.Fatalf("%q gave %d", bad, w.Code)
		}
	}
	w := call(api, "itest-site", "admin", "POST", "/v1/sites", `{"name":"North Plant"}`)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"id":"itest-site-north-plant"`) {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	w2 := call(api, "itest-site", "admin", "POST", "/v1/sites", `{"name":"North Plant"}`)
	if w2.Code != 201 || strings.Contains(w2.Body.String(), `"id":"itest-site-north-plant"`) {
		t.Fatalf("duplicate name must get a distinct id: %d %s", w2.Code, w2.Body.String())
	}
	if l := call(api, "itest-site", "admin", "GET", "/v1/sites", ""); !strings.Contains(l.Body.String(), "North Plant") {
		t.Fatalf("list %s", l.Body.String())
	}
	if l := call(api, "itest-site2", "admin", "GET", "/v1/sites", ""); strings.Contains(l.Body.String(), "North Plant") {
		t.Fatal("site leaked to another tenant")
	}
}
