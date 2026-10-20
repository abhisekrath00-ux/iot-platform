package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestIntegrationEmailStatus(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-em1")
	pool := s.st.Pool
	pool.Exec(t.Context(), `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('em-admin','itest-em1','em@em-test.example','A','admin') ON CONFLICT DO NOTHING`)
	pool.Exec(t.Context(), `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('em-viewer','itest-em1','emv@em-test.example','V','viewer') ON CONFLICT DO NOTHING`)
	clean := func() {
		pool.Exec(t.Context(), `DELETE FROM users WHERE id IN ('em-admin','em-viewer')`)
	}
	clean()
	t.Cleanup(clean)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/notifications/email/status", s.emailStatus)
	get := func(user, role string) (int, string) {
		w := callAs(s.activeUser(mux), "itest-em1", user, role, "GET", "/v1/notifications/email/status", "")
		return w.Code, w.Body.String()
	}
	// Unconfigured deployment: configured:false, no secret values in the payload.
	t.Setenv("SMTP_HOST", "")
	t.Setenv("ALERT_FROM_EMAIL", "")
	c, body := get("em-admin", "admin")
	if c != 200 || !strings.Contains(body, `"configured":false`) {
		t.Fatalf("unconfigured = %d %s, want 200 configured:false", c, body)
	}
	if strings.Contains(body, "SMTP_PASS") || strings.Contains(body, "smtpPass") {
		t.Fatalf("payload must not carry secret material: %s", body)
	}
	// Host without a sender is still not configured (Email() refuses the same pair).
	t.Setenv("SMTP_HOST", "mail.example.internal")
	if c, body = get("em-admin", "admin"); c != 200 || !strings.Contains(body, `"configured":false`) {
		t.Fatalf("host-only = %d %s, want configured:false", c, body)
	}
	// Both set: configured:true.
	t.Setenv("ALERT_FROM_EMAIL", "alerts@example.internal")
	if c, body = get("em-admin", "admin"); c != 200 || !strings.Contains(body, `"configured":true`) {
		t.Fatalf("configured = %d %s, want configured:true", c, body)
	}
	// A viewer cannot read the deployment email status.
	if c, _ = get("em-viewer", "viewer"); c != 403 {
		t.Fatalf("viewer = %d, want 403", c)
	}
}
