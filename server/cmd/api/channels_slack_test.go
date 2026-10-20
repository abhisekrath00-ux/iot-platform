package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIntegrationSlackChannels(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-sl1")
	pool := s.st.Pool
	pool.Exec(t.Context(), `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('sl-admin','itest-sl1','sl@sl-test.example','A','admin') ON CONFLICT DO NOTHING`)
	pool.Exec(t.Context(), `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('sl-viewer','itest-sl1','slv@sl-test.example','V','viewer') ON CONFLICT DO NOTHING`)
	clean := func() {
		pool.Exec(t.Context(), `DELETE FROM users WHERE id IN ('sl-admin','sl-viewer')`)
		pool.Exec(t.Context(), `DELETE FROM audit_log WHERE tenant_id='itest-sl1'`)
		slackChatsAt.Delete("itest-sl1")
	}
	clean()
	t.Cleanup(clean)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/notifications/slack/channels", s.slackChannels)
	get := func(user, role string) (int, string) {
		w := callAs(s.activeUser(mux), "itest-sl1", user, role, "GET", "/v1/notifications/slack/channels", "")
		return w.Code, w.Body.String()
	}
	// Unconfigured deployment: 409 that names the operator step.
	t.Setenv("SLACK_BOT_TOKEN", "")
	slackChatsAt.Delete("itest-sl1")
	if c, b := get("sl-admin", "admin"); c != 409 || !strings.Contains(b, "SLACK_BOT_TOKEN") {
		t.Fatalf("unconfigured = %d %s, want 409 naming SLACK_BOT_TOKEN", c, b)
	}
	// Configured: channels come back with membership and privacy flags.
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		io.WriteString(w, `{"ok":true,"channels":[{"id":"C0123","name":"site-alerts","is_member":true,"is_private":false},{"id":"G0456","name":"ops","is_member":false,"is_private":true}]}`)
	}))
	defer fake.Close()
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-test")
	t.Setenv("SLACK_API_BASE", fake.URL)
	slackChatsAt.Delete("itest-sl1")
	c, body := get("sl-admin", "admin")
	if c != 200 || !strings.Contains(body, "site-alerts") || !strings.Contains(body, "G0456") {
		t.Fatalf("channels = %d %s", c, body)
	}
	// Rate limited like a channel test.
	if c, _ = get("sl-admin", "admin"); c != 429 {
		t.Fatalf("second lookup = %d, want 429", c)
	}
	// Slack API failure (200 + ok:false) surfaces as a friendly 200 payload, not a crash.
	fake2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ok":false,"error":"invalid_auth"}`)
	}))
	defer fake2.Close()
	t.Setenv("SLACK_API_BASE", fake2.URL)
	slackChatsAt.Delete("itest-sl1")
	c, body = get("sl-admin", "admin")
	if c != 200 || !strings.Contains(body, "invalid_auth") {
		t.Fatalf("api error = %d %s", c, body)
	}
	// A viewer cannot list workspace channels.
	slackChatsAt.Delete("itest-sl1")
	if c, _ = get("sl-viewer", "viewer"); c != 403 {
		t.Fatalf("viewer = %d, want 403", c)
	}
}
