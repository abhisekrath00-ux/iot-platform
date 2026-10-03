package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/secrets"
)

// fakeModel is a scripted OpenAI-compatible server. It proves the plumbing, the permission checks
// and the confirm gate. It says nothing about how a real model behaves.
type fakeModel struct {
	mu       sync.Mutex
	script   []map[string]any // each element is the "message" of one reply
	requests []map[string]any
	auth     string
}

func (f *fakeModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, _ := io.ReadAll(r.Body)
	var req map[string]any
	json.Unmarshal(b, &req)
	f.requests = append(f.requests, req)
	f.auth = r.Header.Get("Authorization")
	i := len(f.requests) - 1
	if i >= len(f.script) {
		i = len(f.script) - 1
	}
	json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": f.script[i]}}})
}

func toolMsg(id, name, args string) map[string]any {
	return map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"id": id, "type": "function",
		"function": map[string]any{"name": name, "arguments": args}}}}
}

func TestIntegrationAssistant(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-as")
	seed(t, s, "itest-as2")
	ctx := t.Context()
	pool := s.st.Pool
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	s.secrets = &secrets.Store{Pool: pool, Key: key}
	_ = base64.StdEncoding
	for _, q := range []string{
		`DELETE FROM assistant_actions WHERE tenant_id IN ('itest-as','itest-as2')`,
		`DELETE FROM ai_settings WHERE tenant_id IN ('itest-as','itest-as2')`,
		`DELETE FROM secrets WHERE tenant_id IN ('itest-as','itest-as2')`,
		`DELETE FROM commands WHERE tenant_id='itest-as'`,
		`INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('itest-as-other','itest-as','itest-as-other@example.test','O','operator') ON CONFLICT DO NOTHING`,
		`DELETE FROM alerts WHERE tenant_id='itest-as'`,
		`INSERT INTO alerts(id,tenant_id,severity,message,device_id) VALUES('itest-as-a1','itest-as','warning','Pump vibration','itest-as-dev')`,
		`INSERT INTO commands(request_id,tenant_id,gateway_id,device_id,action,parameters,requested_by) VALUES('itest-as-c1','itest-as','itest-as-gw','itest-as-dev','modbus.write','{}','itest-as-other')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%q: %v", q, err)
		}
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM assistant_actions WHERE tenant_id IN ('itest-as','itest-as2')`)
		pool.Exec(ctx, `DELETE FROM ai_settings WHERE tenant_id IN ('itest-as','itest-as2')`)
		pool.Exec(ctx, `DELETE FROM secrets WHERE tenant_id IN ('itest-as','itest-as2')`)
		pool.Exec(ctx, `DELETE FROM commands WHERE tenant_id='itest-as'`)
		pool.Exec(ctx, `DELETE FROM alerts WHERE tenant_id='itest-as'`)
	})
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/alerts", s.listAlerts)
	api.HandleFunc("POST /v1/alerts/{id}/ack", s.ackAlert)
	api.HandleFunc("POST /v1/commands/{id}/approve", s.approveCommand)
	api.HandleFunc("GET /v1/secrets", s.listSecrets)
	api.HandleFunc("GET /v1/ai/settings", s.getAISettings)
	api.HandleFunc("PUT /v1/ai/settings", s.putAISettings)
	api.HandleFunc("POST /v1/ai/test", s.testAI)
	api.HandleFunc("POST /v1/assistant/chat", s.assistantChat)
	api.HandleFunc("GET /v1/assistant/actions", s.listAssistantActions)
	api.HandleFunc("POST /v1/assistant/actions/{id}/confirm", s.confirmAssistantAction)
	api.HandleFunc("POST /v1/assistant/actions/{id}/reject", s.rejectAssistantAction)
	s.inner = api

	fm := &fakeModel{}
	srv := httptest.NewServer(fm)
	defer srv.Close()
	chat := func(tenant, role, text string) (int, map[string]any) {
		b, _ := json.Marshal(map[string]any{"messages": []any{map[string]string{"role": "user", "content": text}}})
		w := call(api, tenant, role, "POST", "/v1/assistant/chat", string(b))
		var o map[string]any
		json.Unmarshal(w.Body.Bytes(), &o)
		return w.Code, o
	}

	// not connected: refused with the fallback named
	if code, o := chat("itest-as", "admin", "hi"); code != 409 || !strings.Contains(o["fallback"].(string), "/v1/ask") {
		t.Fatalf("not connected: %d %v", code, o)
	}
	// settings: admin only, key write-only, link-local refused
	if w := call(api, "itest-as", "operator", "PUT", "/v1/ai/settings", `{}`); w.Code != 403 {
		t.Fatalf("operator settings: %d", w.Code)
	}
	if w := call(api, "itest-as", "admin", "PUT", "/v1/ai/settings", `{"enabled":true,"base_url":"http://169.254.169.254/v1","model":"m"}`); w.Code != 400 {
		t.Fatalf("metadata address: %d", w.Code)
	}
	w := call(api, "itest-as", "admin", "PUT", "/v1/ai/settings", `{"enabled":true,"base_url":"`+srv.URL+`","model":"fake-1","api_key":"sk-secret-123"}`)
	if w.Code != 200 || strings.Contains(w.Body.String(), "sk-secret-123") || !strings.Contains(w.Body.String(), `"has_key":true`) {
		t.Fatalf("settings: %d %s", w.Code, w.Body.String())
	}
	if w := call(api, "itest-as", "admin", "GET", "/v1/ai/settings", ""); strings.Contains(w.Body.String(), "sk-secret") {
		t.Fatalf("key leaked: %s", w.Body.String())
	}
	var ct string
	pool.QueryRow(ctx, `SELECT encode(ciphertext,'escape') FROM secrets WHERE tenant_id='itest-as' AND name='ai-api-key'`).Scan(&ct)
	if ct == "" || strings.Contains(ct, "sk-secret-123") {
		t.Fatal("the key must be stored encrypted")
	}
	fm.script = []map[string]any{{"role": "assistant", "content": "ok"}}
	if w := call(api, "itest-as", "admin", "POST", "/v1/ai/test", ""); !strings.Contains(w.Body.String(), `"ok":true`) || fm.auth != "Bearer sk-secret-123" {
		t.Fatalf("test call: %s auth=%q", w.Body.String(), fm.auth)
	}

	// the agent run: plan, read, propose an ack, try to approve a command, answer
	fm.mu.Lock()
	fm.requests = nil
	fm.script = []map[string]any{
		toolMsg("c1", "set_plan", `{"steps":["look at open alerts","acknowledge the pump alert"]}`),
		toolMsg("c2", "api_request", `{"method":"GET","path":"/v1/alerts","query":{"status":"open"}}`),
		toolMsg("c3", "api_request", `{"method":"POST","path":"/v1/alerts/itest-as-a1/ack","summary":"Acknowledge the pump vibration alert"}`),
		toolMsg("c4", "api_request", `{"method":"POST","path":"/v1/commands/itest-as-c1/approve"}`),
		toolMsg("c5", "api_request", `{"method":"GET","path":"/v1/secrets"}`),
		{"role": "assistant", "content": "I found one open alert and proposed acknowledging it. It is waiting for your confirmation."},
	}
	fm.mu.Unlock()
	code, o := chat("itest-as", "operator", "Acknowledge the pump alert")
	if code != 200 {
		t.Fatalf("chat: %d %v", code, o)
	}
	plan, _ := o["plan"].([]any)
	trace, _ := o["trace"].([]any)
	pending, _ := o["pending"].([]any)
	if len(plan) != 2 || len(trace) != 5 || len(pending) != 1 {
		t.Fatalf("plan/trace/pending: %v", o)
	}
	tj, _ := json.Marshal(trace)
	if !strings.Contains(string(tj), `"proposed"`) || strings.Count(string(tj), `"refused"`) != 2 {
		t.Fatalf("trace: %s", tj)
	}
	// the read result was fed back to the model
	fm.mu.Lock()
	lastReq, _ := json.Marshal(fm.requests[len(fm.requests)-1])
	fm.mu.Unlock()
	if !strings.Contains(string(lastReq), "Pump vibration") || !strings.Contains(string(lastReq), "awaiting_user_confirmation") {
		t.Fatal("tool results must reach the model")
	}
	if strings.Contains(string(lastReq), "sk-secret") {
		t.Fatal("the model must never see the key")
	}
	// nothing ran: the alert is still open and the command is still pending
	var st string
	pool.QueryRow(ctx, `SELECT status FROM alerts WHERE id='itest-as-a1'`).Scan(&st)
	if st != "open" {
		t.Fatalf("a proposed change ran: %s", st)
	}
	pool.QueryRow(ctx, `SELECT status FROM commands WHERE request_id='itest-as-c1'`).Scan(&st)
	if st != "pending_approval" {
		t.Fatalf("the assistant approved a command: %s", st)
	}
	id := pending[0].(map[string]any)["id"].(string)
	// only the proposing user, in an interactive session, can confirm. test-user is the user in call().
	if w := call(api, "itest-as", "viewer", "POST", "/v1/assistant/actions/"+id+"/confirm", ""); w.Code != 200 {
		// same user id, but a viewer cannot ack: the action runs with the CONFIRMING user's role and fails
		t.Fatalf("confirm call: %d %s", w.Code, w.Body.String())
	} else if !strings.Contains(w.Body.String(), `"status":"failed"`) {
		t.Fatalf("a viewer must not get past the role check: %s", w.Body.String())
	}
	pool.QueryRow(ctx, `SELECT status FROM alerts WHERE id='itest-as-a1'`).Scan(&st)
	if st != "open" {
		t.Fatalf("role check bypassed: %s", st)
	}
	// a second action, confirmed by an operator: runs, and is audited as AI-initiated
	fm.mu.Lock()
	fm.requests = nil
	fm.script = []map[string]any{
		toolMsg("d1", "api_request", `{"method":"POST","path":"/v1/alerts/itest-as-a1/ack","summary":"Acknowledge it"}`),
		{"role": "assistant", "content": "Waiting for confirmation."},
	}
	fm.mu.Unlock()
	_, o = chat("itest-as", "operator", "ack it")
	id2 := o["pending"].([]any)[0].(map[string]any)["id"].(string)
	if w := call(api, "itest-as2", "operator", "POST", "/v1/assistant/actions/"+id2+"/confirm", ""); w.Code != 409 {
		t.Fatalf("another tenant must not confirm: %d", w.Code)
	}
	if w := call(api, "itest-as", "operator", "POST", "/v1/assistant/actions/"+id2+"/confirm", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"executed"`) {
		t.Fatalf("confirm: %d %s", w.Code, w.Body.String())
	}
	if w := call(api, "itest-as", "operator", "POST", "/v1/assistant/actions/"+id2+"/confirm", ""); w.Code != 409 {
		t.Fatalf("second confirm must not run it again: %d", w.Code)
	}
	pool.QueryRow(ctx, `SELECT status FROM alerts WHERE id='itest-as-a1'`).Scan(&st)
	if st != "acknowledged" {
		t.Fatalf("confirmed change did not run: %s", st)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id='itest-as' AND detail->>'ai_initiated'='true'`).Scan(&n)
	if n < 1 {
		t.Fatal("the executed change must be audited as AI-initiated")
	}
	pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id='itest-as' AND action IN ('assistant.run','assistant.confirm')`).Scan(&n)
	if n < 2 {
		t.Fatalf("run and confirm must be audited, got %d", n)
	}
	// a rejected proposal never runs
	fm.mu.Lock()
	fm.requests = nil
	fm.mu.Unlock()
	_, o = chat("itest-as", "operator", "ack it again")
	id3 := o["pending"].([]any)[0].(map[string]any)["id"].(string)
	if w := call(api, "itest-as", "operator", "POST", "/v1/assistant/actions/"+id3+"/reject", ""); w.Code != 200 {
		t.Fatalf("reject: %d", w.Code)
	}
	if w := call(api, "itest-as", "operator", "POST", "/v1/assistant/actions/"+id3+"/confirm", ""); w.Code != 409 {
		t.Fatalf("confirm after reject: %d", w.Code)
	}
}
