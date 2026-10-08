package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestIntegrationWhatsAppChannel(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-wa1")
	pool := s.st.Pool
	clean := func() {
		pool.Exec(t.Context(), `DELETE FROM notification_channels WHERE tenant_id='itest-wa1'`)
		pool.Exec(t.Context(), `DELETE FROM audit_log WHERE tenant_id='itest-wa1'`)
	}
	clean()
	t.Cleanup(clean)
	pool.Exec(t.Context(), `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('wa-admin','itest-wa1','wa@wa-test.example','A','admin') ON CONFLICT DO NOTHING`)
	t.Cleanup(func() { pool.Exec(t.Context(), `DELETE FROM users WHERE id='wa-admin'`) })
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/notifications/channels", s.createChannel)
	mux.HandleFunc("POST /v1/notifications/channels/{id}/test", s.testChannel)
	do := func(path, body string) (int, string) {
		w := callAs(s.activeUser(mux), "itest-wa1", "wa-admin", "admin", "POST", path, body)
		return w.Code, w.Body.String()
	}
	t.Setenv("WHATSAPP_PHONE_NUMBER_ID", "")
	t.Setenv("WHATSAPP_TOKEN", "")
	if c, _ := do("/v1/notifications/channels", `{"type":"whatsapp","target":"+919812345678"}`); c != 409 {
		t.Fatalf("whatsapp unconfigured = %d, want 409", c)
	}
	var gotAuth, gotBody string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotAuth, gotBody = r.Header.Get("Authorization"), string(b)
		w.WriteHeader(200)
		io.WriteString(w, `{"messages":[{"id":"wamid.T"}]}`)
	}))
	defer fake.Close()
	t.Setenv("WHATSAPP_PHONE_NUMBER_ID", "777")
	t.Setenv("WHATSAPP_TOKEN", "tok-wa")
	t.Setenv("WHATSAPP_API_BASE", fake.URL)
	t.Setenv("WHATSAPP_ALLOW_LOOPBACK", "1")
	if c, _ := do("/v1/notifications/channels", `{"type":"whatsapp","target":"98123"}`); c != 400 {
		t.Fatalf("bad number = %d", c)
	}
	c, body := do("/v1/notifications/channels", `{"type":"whatsapp","target":"+919812345678"}`)
	if c != 201 {
		t.Fatalf("create = %d %s", c, body)
	}
	var id string
	pool.QueryRow(t.Context(), `SELECT id FROM notification_channels WHERE tenant_id='itest-wa1' AND type='whatsapp'`).Scan(&id)
	if c, b := do("/v1/notifications/channels/"+id+"/test", ""); c != 200 {
		t.Fatalf("test send = %d %s", c, b)
	}
	if gotAuth != "Bearer tok-wa" || !strings.Contains(gotBody, `"to":"919812345678"`) || !strings.Contains(gotBody, "test message from HexThings") {
		t.Fatalf("delivery: %s %s", gotAuth, gotBody)
	}
}
