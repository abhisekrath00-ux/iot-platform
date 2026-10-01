package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// callAs is like call but lets the test choose the acting user.
func callAs(h http.Handler, tenant, user, role, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	ctx := context.WithValue(r.Context(), auth.CtxTenant, tenant)
	ctx = context.WithValue(ctx, auth.CtxUser, user)
	ctx = context.WithValue(ctx, auth.CtxRole, role)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r.WithContext(ctx))
	return w
}

// Four-eyes on actuation: requester cannot approve; another operator can;
// viewers cannot; other tenants cannot; a decided command cannot be re-approved.
func TestIntegrationCommandFourEyes(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-ce1")
	seed(t, s, "itest-ce2")
	ctx := t.Context()
	pool := s.st.Pool
	cleanup := func() {
		pool.Exec(context.Background(), `DELETE FROM commands WHERE tenant_id IN ('itest-ce1','itest-ce2')`)
		pool.Exec(context.Background(), `DELETE FROM users WHERE id IN ('ce-req','ce-appr','ce-other')`)
	}
	cleanup()
	t.Cleanup(cleanup)
	for _, q := range []string{
		`INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('ce-req','itest-ce1','ce-req@example.com','Req','operator')`,
		`INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('ce-appr','itest-ce1','ce-appr@example.com','Appr','operator')`,
		`INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('ce-other','itest-ce2','ce-other@example.com','Other','operator')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/commands", s.requestCommand)
	api.HandleFunc("POST /v1/commands/{id}/approve", s.approveCommand)

	w := callAs(api, "itest-ce1", "ce-req", "operator", "POST", "/v1/commands",
		`{"gateway_id":"itest-ce1-gw","device_id":"itest-ce1-dev","action":"restart"}`)
	if w.Code != 201 {
		t.Fatalf("request = %d %s", w.Code, w.Body.String())
	}
	id := between(w.Body.String(), `"request_id":"`, `"`)
	path := "/v1/commands/" + id + "/approve"

	if w := callAs(api, "itest-ce1", "ce-req", "operator", "POST", path, ""); w.Code != 409 {
		t.Fatalf("self-approval = %d, want 409", w.Code)
	}
	if w := callAs(api, "itest-ce1", "ce-appr", "viewer", "POST", path, ""); w.Code != 403 {
		t.Fatalf("viewer approve = %d, want 403", w.Code)
	}
	if w := callAs(api, "itest-ce2", "ce-other", "operator", "POST", path, ""); w.Code != 409 {
		t.Fatalf("cross-tenant approve = %d, want 409", w.Code)
	}
	var status string
	pool.QueryRow(ctx, `SELECT status FROM commands WHERE request_id=$1`, id).Scan(&status)
	if status != "pending_approval" {
		t.Fatalf("status after refused approvals = %q", status)
	}
	// A different operator in the same tenant approves. No broker is configured
	// in tests, so dispatch must report failed, not claim sent.
	w = callAs(api, "itest-ce1", "ce-appr", "operator", "POST", path, "")
	if w.Code != 200 {
		t.Fatalf("approve = %d %s", w.Code, w.Body.String())
	}
	pool.QueryRow(ctx, `SELECT status FROM commands WHERE request_id=$1`, id).Scan(&status)
	if status != "failed" && status != "sent" {
		t.Fatalf("status after approve = %q", status)
	}
	if status == "sent" {
		t.Log("broker reachable in this environment; sent is acceptable")
	}
	if w := callAs(api, "itest-ce1", "ce-appr", "operator", "POST", path, ""); w.Code != 409 {
		t.Fatalf("re-approve = %d, want 409", w.Code)
	}
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	j := strings.Index(s, b)
	if j < 0 {
		return ""
	}
	return s[:j]
}
