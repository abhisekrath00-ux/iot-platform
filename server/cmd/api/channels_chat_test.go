package main

import (
	"net/http"
	"testing"
)

func TestIntegrationTeamsSMSChannels(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-tc1")
	pool := s.st.Pool
	clean := func() {
		pool.Exec(t.Context(), `DELETE FROM notification_channels WHERE tenant_id='itest-tc1'`)
		pool.Exec(t.Context(), `DELETE FROM audit_log WHERE tenant_id='itest-tc1'`)
	}
	clean()
	t.Cleanup(clean)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/notifications/channels", s.createChannel)
	post := func(role, body string) int {
		return callAs(s.activeUser(mux), "itest-tc1", map[string]string{"admin": "tc-admin", "viewer": "tc-viewer"}[role], role, "POST", "/v1/notifications/channels", body).Code
	}
	pool.Exec(t.Context(), `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('tc-admin','itest-tc1','tc@tc-test.example','A','admin') ON CONFLICT DO NOTHING`)
	pool.Exec(t.Context(), `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('tc-viewer','itest-tc1','tcv@tc-test.example','V','viewer') ON CONFLICT DO NOTHING`)
	t.Cleanup(func() { pool.Exec(t.Context(), `DELETE FROM users WHERE id IN ('tc-admin','tc-viewer')`) })
	// Teams: https only
	if c := post("admin", `{"type":"teams","target":"http://hooks.example.com/x"}`); c != 400 {
		t.Fatalf("http teams target = %d", c)
	}
	if c := post("admin", `{"type":"teams","target":"https://contoso.webhook.office.com/webhookb2/abc"}`); c != 201 {
		t.Fatalf("teams = %d", c)
	}
	// SMS: refused until the operator configures a gateway; then number-checked
	t.Setenv("SMS_GATEWAY_URL", "")
	if c := post("admin", `{"type":"sms","target":"+4915112345678"}`); c != 409 {
		t.Fatalf("sms without gateway = %d, want 409", c)
	}
	t.Setenv("SMS_GATEWAY_URL", "https://sms.internal.example/send")
	if c := post("admin", `{"type":"sms","target":"12345"}`); c != 400 {
		t.Fatalf("bad number = %d", c)
	}
	if c := post("admin", `{"type":"sms","target":"+4915112345678"}`); c != 201 {
		t.Fatalf("sms = %d", c)
	}
	if c := post("viewer", `{"type":"sms","target":"+4915112345678"}`); c != 403 {
		t.Fatalf("viewer creating a channel = %d", c)
	}
}
