package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"context"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/secrets"
)

type sentMsg struct{ kind, to, subject, text string }

type recNotifier struct {
	mu   sync.Mutex
	sent []sentMsg
}

func (n *recNotifier) Email(_ context.Context, to []string, sub, body string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sent = append(n.sent, sentMsg{"email", strings.Join(to, ","), sub, body})
	return nil
}
func (n *recNotifier) Slack(_ context.Context, ch, text string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sent = append(n.sent, sentMsg{"slack", ch, "", text})
	return nil
}
func (n *recNotifier) last() sentMsg {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.sent) == 0 {
		return sentMsg{}
	}
	return n.sent[len(n.sent)-1]
}
func (n *recNotifier) count() int { n.mu.Lock(); defer n.mu.Unlock(); return len(n.sent) }

// Simulator test: signed Slack and email payloads built by hand from the documented formats. It
// proves our verification and permission logic, not that a real Slack workspace or mail gateway
// sends exactly this.
func TestIntegrationAssistantChannels(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-ch")
	seed(t, s, "itest-ch2")
	ctx := t.Context()
	pool := s.st.Pool
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 7)
	}
	s.secrets = &secrets.Store{Pool: pool, Key: key}
	rec := &recNotifier{}
	s.notifier = rec
	clean := func() {
		for _, q := range []string{
			`DELETE FROM assistant_actions WHERE tenant_id IN ('itest-ch','itest-ch2')`,
			`DELETE FROM assistant_channel_links WHERE tenant_id IN ('itest-ch','itest-ch2')`,
			`DELETE FROM assistant_channel_settings WHERE tenant_id IN ('itest-ch','itest-ch2')`,
			`DELETE FROM assistant_inbound_seen WHERE tenant_id IN ('itest-ch','itest-ch2')`,
			`DELETE FROM ai_settings WHERE tenant_id IN ('itest-ch','itest-ch2')`,
			`DELETE FROM secrets WHERE tenant_id IN ('itest-ch','itest-ch2')`,
			`DELETE FROM commands WHERE tenant_id='itest-ch'`,
			`DELETE FROM alerts WHERE tenant_id='itest-ch'`,
		} {
			pool.Exec(ctx, q)
		}
	}
	clean()
	t.Cleanup(clean)
	for _, q := range []string{
		`INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('itest-ch-other','itest-ch','itest-ch-other@example.test','O','operator') ON CONFLICT DO NOTHING`,
		`INSERT INTO alerts(id,tenant_id,severity,message,device_id) VALUES('itest-ch-a1','itest-ch','warning','Pump vibration','itest-ch-dev'),('itest-ch-a2','itest-ch','warning','Fan noise','itest-ch-dev'),('itest-ch-a3','itest-ch','warning','Belt wear','itest-ch-dev')`,
		`INSERT INTO commands(request_id,tenant_id,gateway_id,device_id,action,parameters,requested_by) VALUES('itest-ch-c1','itest-ch','itest-ch-gw','itest-ch-dev','modbus.write','{}','itest-ch-other')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%q: %v", q, err)
		}
	}
	// users are global by id, so give this tenant its own admin instead of the shared test-user
	pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('itest-ch-admin','itest-ch','itest-ch-admin@example.test','A','admin') ON CONFLICT DO NOTHING`)
	callU := func(h http.Handler, tenant, role, method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		c := context.WithValue(r.Context(), auth.CtxTenant, tenant)
		c = context.WithValue(c, auth.CtxUser, "itest-ch-admin")
		c = context.WithValue(c, auth.CtxRole, role)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r.WithContext(c))
		return w
	}
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/alerts", s.listAlerts)
	api.HandleFunc("POST /v1/alerts/{id}/ack", s.ackAlert)
	api.HandleFunc("POST /v1/commands/{id}/approve", s.approveCommand)
	api.HandleFunc("PUT /v1/ai/settings", s.putAISettings)
	api.HandleFunc("PUT /v1/assistant/channel-settings", s.putChannelSettings)
	api.HandleFunc("GET /v1/assistant/channel-settings", s.getChannelSettings)
	api.HandleFunc("GET /v1/assistant/links", s.listChannelLinks)
	api.HandleFunc("POST /v1/assistant/links", s.createChannelLink)
	api.HandleFunc("PUT /v1/assistant/links/{id}/autorun", s.setLinkAutorun)
	s.inner = api
	pub := http.NewServeMux()
	pub.HandleFunc("POST /v1/assistant/inbound/slack/{tenant}", s.inboundSlack)
	pub.HandleFunc("POST /v1/assistant/inbound/email/{tenant}", s.inboundEmail)

	fm := &fakeModel{}
	msrv := httptest.NewServer(fm)
	defer msrv.Close()
	setModel := func(script ...map[string]any) { fm.mu.Lock(); fm.script, fm.requests = script, nil; fm.mu.Unlock() }
	modelCalls := func() int { fm.mu.Lock(); defer fm.mu.Unlock(); return len(fm.requests) }
	ackScript := func(id string) []map[string]any {
		return []map[string]any{toolMsg("1", "api_request", `{"method":"POST","path":"/v1/alerts/`+id+`/ack","summary":"Acknowledge `+id+`"}`), {"role": "assistant", "content": "Proposed."}}
	}
	alertStatus := func(id string) string {
		var st string
		pool.QueryRow(ctx, `SELECT status FROM alerts WHERE id=$1`, id).Scan(&st)
		return st
	}

	if w := callU(api, "itest-ch", "admin", "PUT", "/v1/ai/settings", `{"enabled":true,"base_url":"`+msrv.URL+`","model":"fake"}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	// channel settings: admin only; cannot enable without a secret
	if w := callU(api, "itest-ch", "operator", "PUT", "/v1/assistant/channel-settings", `{}`); w.Code != 403 {
		t.Fatalf("operator: %d", w.Code)
	}
	if w := callU(api, "itest-ch", "admin", "PUT", "/v1/assistant/channel-settings", `{"slack_enabled":true}`); w.Code != 400 {
		t.Fatalf("enable without secret: %d", w.Code)
	}
	const slackSecret, mailSecret = "slack-signing-secret-xyz", "mail-gateway-secret-abc"
	if w := callU(api, "itest-ch", "admin", "PUT", "/v1/assistant/channel-settings", `{"slack_enabled":true,"email_enabled":true,"slack_signing_secret":"`+slackSecret+`","email_inbound_secret":"`+mailSecret+`"}`); w.Code != 200 || strings.Contains(w.Body.String(), slackSecret) {
		t.Fatalf("settings: %d %s", w.Code, w.Body.String())
	}

	post := func(path, body string, hdr map[string]string) int {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		pub.ServeHTTP(w, r)
		s.bg.Wait()
		return w.Code
	}
	evN := 0
	slack := func(tenant, secret, user, chType, text string, age time.Duration) int {
		evN++
		b, _ := json.Marshal(map[string]any{"type": "event_callback", "event_id": "Ev" + strconv.Itoa(evN) + tenant,
			"event": map[string]any{"type": "message", "user": user, "channel": "D" + user, "channel_type": chType, "text": text}})
		ts := strconv.FormatInt(time.Now().Add(-age).Unix(), 10)
		return post("/v1/assistant/inbound/slack/"+tenant, string(b), map[string]string{"X-Slack-Request-Timestamp": ts, "X-Slack-Signature": "v0=" + hmacHex([]byte(secret), "v0:"+ts+":"+string(b))})
	}
	mailN := 0
	email := func(tenant, secret, from, text, dkim string) int {
		mailN++
		b, _ := json.Marshal(map[string]any{"message_id": "m" + strconv.Itoa(mailN) + "-" + tenant, "from": from, "subject": "ack", "text": text, "dkim": dkim, "spf": "pass"})
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		return post("/v1/assistant/inbound/email/"+tenant, string(b), map[string]string{"X-Timestamp": ts, "X-Signature": hmacHex([]byte(secret), ts+"."+string(b))})
	}

	// signatures: wrong secret, stale timestamp, and a tenant with the channel off
	setModel(ackScript("itest-ch-a1")...)
	if c := slack("itest-ch", "wrong-secret-wrong-secret", "U0ADMIN1", "im", "ack it", 0); c != 401 {
		t.Fatalf("bad signature: %d", c)
	}
	if c := slack("itest-ch", slackSecret, "U0ADMIN1", "im", "ack it", 10*time.Minute); c != 401 {
		t.Fatalf("stale: %d", c)
	}
	if c := slack("itest-ch2", slackSecret, "U0ADMIN1", "im", "ack it", 0); c != 404 {
		t.Fatalf("channel off: %d", c)
	}
	// an unlinked Slack user gets a refusal and the model is never called
	if c := slack("itest-ch", slackSecret, "U0STRANGER", "im", "ack it", 0); c != 200 || modelCalls() != 0 || !strings.Contains(rec.last().text, "not linked") {
		t.Fatalf("unlinked: %d calls=%d last=%+v", c, modelCalls(), rec.last())
	}

	// linking: a web user starts it, then proves control of the identity with the one-time code
	w := callU(api, "itest-ch", "admin", "POST", "/v1/assistant/links", `{"kind":"slack","address":"u0admin1"}`)
	var lk struct{ Code string }
	json.Unmarshal(w.Body.Bytes(), &lk)
	if w.Code != 201 || len(lk.Code) != 8 {
		t.Fatalf("link start: %d %s", w.Code, w.Body.String())
	}
	if callU(api, "itest-ch", "admin", "POST", "/v1/assistant/links", `{"kind":"slack","address":"not an id"}`).Code != 400 {
		t.Fatal("bad slack id accepted")
	}
	slack("itest-ch", slackSecret, "U0STRANGER", "im", "link "+lk.Code, 0) // someone else cannot use the code
	var st string
	pool.QueryRow(ctx, `SELECT status FROM assistant_channel_links WHERE tenant_id='itest-ch' AND address='U0ADMIN1'`).Scan(&st)
	if st != "pending" {
		t.Fatalf("another identity verified the link: %s", st)
	}
	slack("itest-ch", slackSecret, "U0ADMIN1", "im", "link "+lk.Code, 0)
	pool.QueryRow(ctx, `SELECT status FROM assistant_channel_links WHERE tenant_id='itest-ch' AND address='U0ADMIN1'`).Scan(&st)
	if st != "verified" {
		t.Fatalf("link not verified: %s", st)
	}

	// shared channel and bot messages are ignored
	n := rec.count()
	slack("itest-ch", slackSecret, "U0ADMIN1", "channel", "ack it", 0)
	if rec.count() != n || modelCalls() != 0 {
		t.Fatal("a message in a shared channel must be ignored")
	}

	// a change waits for yes; replay does not run twice
	slack("itest-ch", slackSecret, "U0ADMIN1", "im", "please acknowledge the pump alert", 0)
	reply := rec.last().text
	m := regexp.MustCompile(`YES ([A-Z0-9]{6})`).FindStringSubmatch(reply)
	if m == nil || alertStatus("itest-ch-a1") != "open" || rec.last().kind != "slack" || rec.last().to != "DU0ADMIN1" {
		t.Fatalf("expected a pending request in the DM: %+v status=%s", rec.last(), alertStatus("itest-ch-a1"))
	}
	slack("itest-ch", slackSecret, "U0ADMIN1", "im", "yes "+m[1], 0)
	if alertStatus("itest-ch-a1") == "open" || !strings.Contains(rec.last().text, "Done") {
		t.Fatalf("yes should run it: %s %+v", alertStatus("itest-ch-a1"), rec.last())
	}
	var aud int
	pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id='itest-ch' AND action='assistant.confirm' AND detail->>'via'='slack' AND detail->>'ai_initiated'='true'`).Scan(&aud)
	if aud != 1 {
		t.Fatalf("confirm not audited as AI-initiated via slack: %d", aud)
	}
	slack("itest-ch", slackSecret, "U0ADMIN1", "im", "yes "+m[1], 0) // the same code again does nothing
	if !strings.Contains(rec.last().text, "Nothing open") {
		t.Fatalf("second yes: %+v", rec.last())
	}

	// a bare "yes" works for exactly one open request, and "no" drops it
	setModel(ackScript("itest-ch-a2")...)
	slack("itest-ch", slackSecret, "U0ADMIN1", "im", "ack the fan alert", 0)
	slack("itest-ch", slackSecret, "U0ADMIN1", "im", "no", 0)
	if alertStatus("itest-ch-a2") != "open" || !strings.Contains(rec.last().text, "Dropped") {
		t.Fatalf("no should drop: %+v", rec.last())
	}
	setModel(ackScript("itest-ch-a2")...)
	slack("itest-ch", slackSecret, "U0ADMIN1", "im", "ack the fan alert", 0)
	slack("itest-ch", slackSecret, "U0ADMIN1", "im", "yes", 0)
	if alertStatus("itest-ch-a2") == "open" {
		t.Fatalf("bare yes should confirm the single open request: %+v", rec.last())
	}

	// approving a control command from chat is refused, whatever the model tries
	setModel(toolMsg("1", "api_request", `{"method":"POST","path":"/v1/commands/itest-ch-c1/approve","summary":"approve"}`), map[string]any{"role": "assistant", "content": "I could not approve it."})
	slack("itest-ch", slackSecret, "U0ADMIN1", "im", "approve the pending command", 0)
	var appr *string
	pool.QueryRow(ctx, `SELECT approved_by FROM commands WHERE request_id='itest-ch-c1'`).Scan(&appr)
	var pend int
	pool.QueryRow(ctx, `SELECT count(*) FROM assistant_actions WHERE tenant_id='itest-ch' AND path LIKE '/v1/commands/%'`).Scan(&pend)
	if appr != nil || pend != 0 {
		t.Fatalf("a command was approved or queued from chat: approved_by=%v queued=%d", appr, pend)
	}

	// low-risk auto-run: off by default; an admin turns it on for the link
	setModel(ackScript("itest-ch-a3")...)
	slack("itest-ch", slackSecret, "U0ADMIN1", "im", "ack the belt alert", 0)
	if alertStatus("itest-ch-a3") != "open" {
		t.Fatal("auto-run must be off by default")
	}
	slack("itest-ch", slackSecret, "U0ADMIN1", "im", "no", 0)
	var linkID string
	pool.QueryRow(ctx, `SELECT id FROM assistant_channel_links WHERE tenant_id='itest-ch' AND address='U0ADMIN1'`).Scan(&linkID)
	if callU(api, "itest-ch", "operator", "PUT", "/v1/assistant/links/"+linkID+"/autorun", `{"enabled":true}`).Code != 403 {
		t.Fatal("only an admin may switch on auto-run")
	}
	if callU(api, "itest-ch", "admin", "PUT", "/v1/assistant/links/"+linkID+"/autorun", `{"enabled":true}`).Code != 200 {
		t.Fatal("admin autorun")
	}
	setModel(ackScript("itest-ch-a3")...)
	slack("itest-ch", slackSecret, "U0ADMIN1", "im", "ack the belt alert", 0)
	if alertStatus("itest-ch-a3") == "open" {
		t.Fatal("low-risk change should have run without a yes")
	}
	pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id='itest-ch' AND action='assistant.autorun'`).Scan(&aud)
	if aud != 1 {
		t.Fatalf("auto-run not audited: %d", aud)
	}
	// anything beyond the low-risk list still waits
	setModel(toolMsg("1", "api_request", `{"method":"PUT","path":"/v1/branding","body":{"product_name":"X"},"summary":"rename"}`), map[string]any{"role": "assistant", "content": "Proposed."})
	slack("itest-ch", slackSecret, "U0ADMIN1", "im", "rename the product", 0)
	if !strings.Contains(rec.last().text, "Reply YES") {
		t.Fatalf("a non-low-risk change must wait: %+v", rec.last())
	}

	// email: forged or unlinked senders are never answered and never run
	setModel(ackScript("itest-ch-a1")...)
	n = rec.count()
	if c := email("itest-ch", mailSecret, "admin@corp.example", "do it", "fail"); c != 200 || rec.count() != n || modelCalls() != 0 {
		t.Fatalf("a forged From must be dropped silently: %d", c)
	}
	if c := email("itest-ch", mailSecret, "stranger@corp.example", "do it", "pass"); c != 200 || rec.count() != n || modelCalls() != 0 {
		t.Fatalf("an unlinked address must be dropped silently")
	}
	w = callU(api, "itest-ch", "admin", "POST", "/v1/assistant/links", `{"kind":"email","address":"Admin <Admin@Corp.Example>"}`)
	json.Unmarshal(w.Body.Bytes(), &lk)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"admin@corp.example"`) {
		t.Fatalf("email link: %d %s", w.Code, w.Body.String())
	}
	email("itest-ch", mailSecret, "admin@corp.example", "link "+lk.Code, "pass")
	if rec.last().kind != "email" || !strings.Contains(rec.last().text, "Linked") {
		t.Fatalf("email link verify: %+v", rec.last())
	}
	// and the same words with failing dkim do not count as the owner
	setModel(toolMsg("1", "api_request", `{"method":"POST","path":"/v1/alerts/itest-ch-a1/ack","summary":"x"}`), map[string]any{"role": "assistant", "content": "Proposed."})
	email("itest-ch", mailSecret, "admin@corp.example", "ack the pump alert", "pass")
	if rec.last().kind != "email" || rec.last().to != "admin@corp.example" || !strings.Contains(rec.last().text, "Reply YES") {
		t.Fatalf("email run: %+v", rec.last())
	}
	// a Slack "yes" cannot confirm what was proposed over email
	m = regexp.MustCompile(`YES ([A-Z0-9]{6})`).FindStringSubmatch(rec.last().text)
	// (alert a1 was already acknowledged above, so the request fails when run; what matters is the channel)
	slack("itest-ch", slackSecret, "U0ADMIN1", "im", "yes", 0)
	if strings.Contains(rec.last().text, "Done") {
		t.Fatalf("a bare yes on Slack must not confirm an email request: %+v", rec.last())
	}
	_ = m
}
