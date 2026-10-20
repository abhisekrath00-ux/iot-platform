package main

import (
	"context"
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

func TestIntegrationTelegramChannel(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-tg1")
	pool := s.st.Pool
	clean := func() {
		pool.Exec(t.Context(), `DELETE FROM notification_channels WHERE tenant_id='itest-tg1'`)
		pool.Exec(t.Context(), `DELETE FROM audit_log WHERE tenant_id='itest-tg1'`)
		pool.Exec(t.Context(), `DELETE FROM telegram_chats WHERE tenant_id='itest-tg1'`)
	}
	clean()
	t.Cleanup(clean)
	pool.Exec(t.Context(), `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('tg-admin','itest-tg1','tg@tg-test.example','A','admin') ON CONFLICT DO NOTHING`)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM users WHERE id='tg-admin'`) })
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/notifications/channels", s.createChannel)
	mux.HandleFunc("POST /v1/notifications/channels/{id}/test", s.testChannel)
	do := func(path, body string) (int, string) {
		w := callAs(s.activeUser(mux), "itest-tg1", "tg-admin", "admin", "POST", path, body)
		return w.Code, w.Body.String()
	}
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	if c, _ := do("/v1/notifications/channels", `{"type":"telegram","target":"123456789"}`); c != 409 {
		t.Fatalf("telegram unconfigured = %d, want 409", c)
	}
	var gotBody string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(200)
		io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
	}))
	defer fake.Close()
	t.Setenv("TELEGRAM_BOT_TOKEN", "tok-tg")
	t.Setenv("TELEGRAM_API_BASE", fake.URL)
	t.Setenv("TELEGRAM_ALLOW_LOOPBACK", "1")
	if c, _ := do("/v1/notifications/channels", `{"type":"telegram","target":"-1001234567890"}`); c != 400 {
		t.Fatalf("unpaired chat must be refused = %d, want 400", c)
	}
	pool.Exec(t.Context(), `INSERT INTO telegram_chats(tenant_id,chat_id,title) VALUES('itest-tg1','-1001234567890','paired test chat')`)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM telegram_chats WHERE tenant_id='itest-tg1'`) })
	if c, _ := do("/v1/notifications/channels", `{"type":"telegram","target":"@somechannel"}`); c != 400 {
		t.Fatalf("bad chat id = %d", c)
	}
	c, body := do("/v1/notifications/channels", `{"type":"telegram","target":"-1001234567890"}`)
	if c != 201 {
		t.Fatalf("create = %d %s", c, body)
	}
	var id string
	var enabled bool
	pool.QueryRow(t.Context(), `SELECT id, enabled FROM notification_channels WHERE tenant_id='itest-tg1' AND type='telegram'`).Scan(&id, &enabled)
	if enabled {
		t.Fatal("a new channel must be staged disabled until an admin enables it")
	}
	if c, b := do("/v1/notifications/channels/"+id+"/test", ""); c != 200 {
		t.Fatalf("test send = %d %s", c, b)
	}
	if !strings.Contains(gotBody, `"chat_id":"-1001234567890"`) || !strings.Contains(gotBody, "test message from HexThings") {
		t.Fatalf("delivery: %s", gotBody)
	}
}

func TestIntegrationTelegramPairing(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-tg2")
	seed(t, s, "itest-tg3")
	pool := s.st.Pool
	pool.Exec(t.Context(), `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('tg2-admin','itest-tg2','tg2@tg-test.example','A','admin') ON CONFLICT DO NOTHING`)
	pool.Exec(t.Context(), `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('tg3-admin','itest-tg3','tg3@tg-test.example','A','admin') ON CONFLICT DO NOTHING`)
	clean2 := func() {
		pool.Exec(context.Background(), `DELETE FROM users WHERE id IN ('tg2-admin','tg3-admin')`)
		pool.Exec(context.Background(), `DELETE FROM telegram_chats WHERE tenant_id IN ('itest-tg2','itest-tg3')`)
		pool.Exec(context.Background(), `DELETE FROM telegram_pair_codes WHERE tenant_id IN ('itest-tg2','itest-tg3')`)
		pool.Exec(context.Background(), `DELETE FROM notification_channels WHERE tenant_id IN ('itest-tg2','itest-tg3')`)
		pool.Exec(context.Background(), `DELETE FROM audit_log WHERE tenant_id IN ('itest-tg2','itest-tg3')`)
	}
	clean2()
	t.Cleanup(clean2)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/notifications/telegram/chats", s.telegramChats)
	mux.HandleFunc("POST /v1/notifications/telegram/pair-code", s.telegramPairCode)
	mux.HandleFunc("POST /v1/notifications/channels", s.createChannel)
	get := func(tenant, user, role, method, path, body string) (int, string) {
		w := callAs(s.activeUser(mux), tenant, user, role, method, path, body)
		return w.Code, w.Body.String()
	}
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	if c, _ := get("itest-tg2", "tg2-admin", "admin", "POST", "/v1/notifications/telegram/pair-code", ""); c != 409 {
		t.Fatalf("pair code unconfigured = %d, want 409", c)
	}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		io.WriteString(w, `{"ok":true,"result":[
			{"message":{"text":"hello","chat":{"id":-1001111111111,"type":"supergroup","title":"Other Tenant Group"}}}
		]}`)
	}))
	defer fake.Close()
	t.Setenv("TELEGRAM_BOT_TOKEN", "tok-tg")
	t.Setenv("TELEGRAM_API_BASE", fake.URL)
	t.Setenv("TELEGRAM_ALLOW_LOOPBACK", "1")

	c, body := get("itest-tg2", "tg2-admin", "admin", "POST", "/v1/notifications/telegram/pair-code", "")
	if c != 200 || !strings.Contains(body, `"code"`) {
		t.Fatalf("pair code = %d %s", c, body)
	}
	var code string
	pool.QueryRow(t.Context(), `SELECT code FROM telegram_pair_codes WHERE tenant_id='itest-tg2'`).Scan(&code)

	// The bot sees another tenant's group chattering; without a spoken code nothing pairs.
	telegramChatsAt.Delete("itest-tg2")
	c, body = get("itest-tg2", "tg2-admin", "admin", "GET", "/v1/notifications/telegram/chats", "")
	if c != 200 || strings.Contains(body, "Other Tenant Group") {
		t.Fatalf("unpaired chats must not be listed: %d %s", c, body)
	}

	// The other tenant's admin cannot claim tg2's chat by id, even if they know it.
	pool.Exec(t.Context(), `INSERT INTO telegram_chats(tenant_id,chat_id,title) VALUES('itest-tg2','-1001234567890','Plant Ops')`)
	if c, _ = get("itest-tg3", "tg3-admin", "admin", "POST", "/v1/notifications/channels", `{"type":"telegram","target":"-1001234567890"}`); c != 400 {
		t.Fatalf("cross-tenant chat id must be refused = %d, want 400", c)
	}
	telegramChatsAt.Delete("itest-tg3")
	c, body = get("itest-tg3", "tg3-admin", "admin", "GET", "/v1/notifications/telegram/chats", "")
	if c != 200 || strings.Contains(body, "Plant Ops") {
		t.Fatalf("tenant isolation: %d %s", c, body)
	}

	// tg2 speaks its code in its own chat: that chat pairs, the code is consumed.
	fake2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		io.WriteString(w, `{"ok":true,"result":[
			{"message":{"text":"/pair `+code+`","chat":{"id":-1009876543210,"type":"supergroup","title":"Boiler Room"}}},
			{"message":{"text":"hello","chat":{"id":-1001111111111,"type":"supergroup","title":"Other Tenant Group"}}}
		]}`)
	}))
	defer fake2.Close()
	t.Setenv("TELEGRAM_API_BASE", fake2.URL)
	pool.Exec(t.Context(), `DELETE FROM telegram_chats WHERE tenant_id='itest-tg2'`)
	telegramChatsAt.Delete("itest-tg2")
	c, body = get("itest-tg2", "tg2-admin", "admin", "GET", "/v1/notifications/telegram/chats", "")
	if c != 200 || !strings.Contains(body, "Boiler Room") || strings.Contains(body, "Other Tenant Group") {
		t.Fatalf("pairing scan: %d %s", c, body)
	}
	var codeLeft int
	pool.QueryRow(t.Context(), `SELECT count(*) FROM telegram_pair_codes WHERE tenant_id='itest-tg2'`).Scan(&codeLeft)
	if codeLeft != 0 {
		t.Fatal("pair code must be single use")
	}
	if c, b := get("itest-tg2", "tg2-admin", "admin", "POST", "/v1/notifications/channels", `{"type":"telegram","target":"-1009876543210"}`); c != 201 {
		t.Fatalf("paired chat channel = %d %s", c, b)
	}
	// rate limit still applies
	if c, _ = get("itest-tg2", "tg2-admin", "admin", "GET", "/v1/notifications/telegram/chats", ""); c != 429 {
		t.Fatalf("second scan = %d, want 429", c)
	}
	if c, _ = get("itest-tg2", "tg2-viewer", "viewer", "GET", "/v1/notifications/telegram/chats", ""); c != 403 {
		t.Fatalf("viewer = %d, want 403", c)
	}
}
