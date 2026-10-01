package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// An operator API key may request but must never approve a command.
func TestIntegrationAPIKeyCannotApproveCommand(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-ak3")
	s.st.Pool.Exec(t.Context(), `DELETE FROM api_keys WHERE tenant_id='itest-ak3'`)
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/api-keys", s.createAPIKey)
	api.HandleFunc("POST /v1/commands/{id}/approve", s.approveCommand)
	t.Cleanup(func() { s.st.Pool.Exec(t.Context(), `DELETE FROM api_keys WHERE tenant_id='itest-ak3'`) })

	w := call(api, "itest-ak3", "admin", "POST", "/v1/api-keys", `{"name":"auto","role":"operator","expires_in_days":30}`)
	if w.Code != 201 {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	var k struct{ Token string }
	json.Unmarshal(w.Body.Bytes(), &k)

	r := httptest.NewRequest("POST", "/v1/commands/nope/approve", nil)
	r.Header.Set("Authorization", "Bearer "+k.Token)
	rec := httptest.NewRecorder()
	auth_mw(s, api).ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Fatalf("operator key approve = %d, want 403", rec.Code)
	}
}
