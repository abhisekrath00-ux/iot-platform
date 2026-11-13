package main

import (
	"context"
	"encoding/json"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIntegrationAddSourceKeepsGatewayAndIdentity(t *testing.T) {
	s, _ := testServer(t)
	tenant := "itest-existing-source"
	seed(t, s, tenant)
	m := http.NewServeMux()
	m.HandleFunc("POST /v1/gateways/{id}/source-setup", s.createExistingSourceSession)
	counts := func() (int, int) {
		var gw, tokens int
		s.st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM gateways WHERE tenant_id=$1`, tenant).Scan(&gw)
		s.st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM enrollment_tokens WHERE tenant_id=$1`, tenant).Scan(&tokens)
		return gw, tokens
	}
	beforeGW, beforeTokens := counts()
	path := "/v1/gateways/" + tenant + "-gw/source-setup"
	run := func(role, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader("{}"))
		c := context.WithValue(r.Context(), auth.CtxTenant, tenant)
		c = context.WithValue(c, auth.CtxRole, role)
		c = context.WithValue(c, auth.CtxUser, "test-user")
		r = r.WithContext(c)
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		return w
	}
	var first string
	for i := 0; i < 2; i++ {
		w := run("admin", "same-request")
		if w.Code != 201 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		if out["gateway_id"] != tenant+"-gw" || out["gateway_status"] != "active" || out["claim_code"] != nil {
			t.Fatal(out)
		}
		if i == 0 {
			first = out["session_id"].(string)
		} else if first != out["session_id"] {
			t.Fatal("replay created second source session")
		}
	}
	if w := run("viewer", "same-request"); w.Code != 403 {
		t.Fatal("role replay bypass", w.Code)
	}
	if w := call(m, "foreign", "admin", "POST", path, "{}"); w.Code != 404 {
		t.Fatal("tenant leak", w.Code)
	}
	gw, tokens := counts()
	if gw != beforeGW || tokens != beforeTokens {
		t.Fatal("gateway/claim was duplicated")
	}
	var n int
	s.st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM commissioning_sessions WHERE tenant_id=$1`, tenant).Scan(&n)
	if n != 1 {
		t.Fatal("duplicate session", n)
	}
}
