package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIntegrationAPIKeyScopes(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-ak4")
	s.st.Pool.Exec(t.Context(), `DELETE FROM api_keys WHERE tenant_id='itest-ak4'`)
	t.Cleanup(func() { s.st.Pool.Exec(t.Context(), `DELETE FROM api_keys WHERE tenant_id='itest-ak4'`) })
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/api-keys", s.createAPIKey)
	api.HandleFunc("GET /v1/api-keys", s.listAPIKeys)
	api.HandleFunc("GET /v1/devices", s.listDevices)
	api.HandleFunc("GET /v1/alerts", s.listAlerts)

	for _, bad := range []string{`{"name":"k","scopes":["api-keys"],"expires_in_days":30}`, `{"name":"k","scopes":["commands"],"expires_in_days":30}`,
		`{"name":"k","scopes":["devices","devices"],"expires_in_days":30}`, `{"name":"k","scopes":["nope"],"expires_in_days":30}`} {
		if w := call(api, "itest-ak4", "admin", "POST", "/v1/api-keys", bad); w.Code != 400 {
			t.Fatalf("%s = %d, want 400", bad, w.Code)
		}
	}
	w := call(api, "itest-ak4", "admin", "POST", "/v1/api-keys", `{"name":"devices only","scopes":["devices"],"expires_in_days":30}`)
	if w.Code != 201 {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	var k struct{ Token string }
	json.Unmarshal(w.Body.Bytes(), &k)
	use := func(path string) int {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+k.Token)
		rec := httptest.NewRecorder()
		auth_mw(s, api).ServeHTTP(rec, r)
		return rec.Code
	}
	if c := use("/v1/devices"); c != 200 {
		t.Fatalf("in-scope = %d", c)
	}
	if c := use("/v1/alerts"); c != 403 {
		t.Fatalf("out-of-scope = %d, want 403", c)
	}
	if l := call(api, "itest-ak4", "admin", "GET", "/v1/api-keys", ""); !contains(l.Body.String(), `"scopes":["devices"]`) {
		t.Fatalf("list lacks scopes: %s", l.Body.String())
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
