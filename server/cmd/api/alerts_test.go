package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestIntegrationAlertLifecycle(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-al1")
	seed(t, s, "itest-al2")
	for _, q := range []string{
		`INSERT INTO alerts(id,tenant_id,severity,message) VALUES('itest-al1-a','itest-al1','critical','hot') ON CONFLICT (id) DO UPDATE SET status='open', acknowledged_by=NULL, acknowledged_at=NULL, resolved_by=NULL, resolved_at=NULL`,
	} {
		if _, err := s.st.Pool.Exec(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/alerts", s.listAlerts)
	api.HandleFunc("GET /v1/alerts/{id}", s.getAlert)
	api.HandleFunc("POST /v1/alerts/{id}/ack", s.ackAlert)
	api.HandleFunc("POST /v1/alerts/{id}/resolve", s.resolveAlert)
	api.HandleFunc("POST /v1/alerts/{id}/comments", s.commentAlert)

	// viewers cannot act
	if w := call(api, "itest-al1", "viewer", "POST", "/v1/alerts/itest-al1-a/ack", ""); w.Code != 403 {
		t.Fatalf("viewer ack = %d, want 403", w.Code)
	}
	// another tenant cannot touch it
	if w := call(api, "itest-al2", "admin", "POST", "/v1/alerts/itest-al1-a/ack", ""); w.Code != 409 {
		t.Fatalf("cross-tenant ack = %d, want 409", w.Code)
	}
	if w := call(api, "itest-al2", "admin", "POST", "/v1/alerts/itest-al1-a/comments", `{"body":"x"}`); w.Code != 404 {
		t.Fatalf("cross-tenant comment = %d, want 404", w.Code)
	}
	if w := call(api, "itest-al2", "admin", "GET", "/v1/alerts/itest-al1-a", ""); w.Code != 404 {
		t.Fatalf("cross-tenant get = %d, want 404", w.Code)
	}
	// open -> ack ok, second ack conflicts
	if w := call(api, "itest-al1", "operator", "POST", "/v1/alerts/itest-al1-a/ack", ""); w.Code != 200 {
		t.Fatalf("ack = %d %s", w.Code, w.Body.String())
	}
	if w := call(api, "itest-al1", "operator", "POST", "/v1/alerts/itest-al1-a/ack", ""); w.Code != 409 {
		t.Fatalf("double ack = %d, want 409", w.Code)
	}
	// comments validated
	if w := call(api, "itest-al1", "operator", "POST", "/v1/alerts/itest-al1-a/comments", `{"body":"  "}`); w.Code != 400 {
		t.Fatalf("empty comment = %d, want 400", w.Code)
	}
	if w := call(api, "itest-al1", "operator", "POST", "/v1/alerts/itest-al1-a/comments", `{"body":"checking the boiler"}`); w.Code != 201 {
		t.Fatalf("comment = %d", w.Code)
	}
	// resolve is final
	if w := call(api, "itest-al1", "admin", "POST", "/v1/alerts/itest-al1-a/resolve", ""); w.Code != 200 {
		t.Fatalf("resolve = %d", w.Code)
	}
	if w := call(api, "itest-al1", "admin", "POST", "/v1/alerts/itest-al1-a/ack", ""); w.Code != 409 {
		t.Fatalf("ack after resolve = %d, want 409", w.Code)
	}
	if w := call(api, "itest-al1", "admin", "POST", "/v1/alerts/itest-al1-a/resolve", ""); w.Code != 409 {
		t.Fatalf("double resolve = %d, want 409", w.Code)
	}
	w := call(api, "itest-al1", "viewer", "GET", "/v1/alerts/itest-al1-a", "")
	b := w.Body.String()
	if w.Code != 200 || !strings.Contains(b, `"status":"resolved"`) || !strings.Contains(b, "checking the boiler") || !strings.Contains(b, `"acknowledged_by":"test-user"`) {
		t.Fatalf("detail wrong: %d %s", w.Code, b)
	}
	// status filter
	if w := call(api, "itest-al1", "viewer", "GET", "/v1/alerts?status=open", ""); strings.Contains(w.Body.String(), "itest-al1-a") {
		t.Fatalf("resolved alert listed as open: %s", w.Body.String())
	}
	if w := call(api, "itest-al1", "viewer", "GET", "/v1/alerts?status=bogus", ""); w.Code != 400 {
		t.Fatalf("bad status = %d, want 400", w.Code)
	}
}
