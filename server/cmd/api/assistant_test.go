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
	t.Setenv("AI_TOOL_MODE", "generic") // these scripts drive the generic api_request tool; the typed mode has its own tests
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
		`DELETE FROM ai_profiles WHERE tenant_id IN ('itest-as','itest-as2')`,
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
	api.HandleFunc("GET /v1/reports", s.listReports)
	api.HandleFunc("GET /v1/reports/{id}/download", s.downloadReport)
	api.HandleFunc("GET /v1/alerts", s.listAlerts)
	api.HandleFunc("POST /v1/alerts/{id}/ack", s.ackAlert)
	api.HandleFunc("POST /v1/commands/{id}/approve", s.approveCommand)
	api.HandleFunc("GET /v1/secrets", s.listSecrets)
	api.HandleFunc("GET /v1/ai/settings", s.getAISettings)
	api.HandleFunc("PUT /v1/ai/settings", s.putAISettings)
	api.HandleFunc("POST /v1/ai/test", s.testAI)
	api.HandleFunc("GET /v1/ai/profiles", s.listAIProfiles)
	api.HandleFunc("POST /v1/ai/profiles", s.saveAIProfile)
	api.HandleFunc("PUT /v1/ai/profiles/{id}", s.saveAIProfile)
	api.HandleFunc("DELETE /v1/ai/profiles/{id}", s.deleteAIProfile)
	api.HandleFunc("POST /v1/ai/profiles/{id}/activate", s.activateAIProfile)
	api.HandleFunc("POST /v1/ai/profiles/{id}/test", s.testAIProfile)
	api.HandleFunc("GET /v1/ai/activity", s.aiActivity)
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

	// provider profiles: admin only, key write-only and encrypted, switch without re-entering the key
	if w := call(api, "itest-as", "operator", "GET", "/v1/ai/profiles", ""); w.Code != 403 {
		t.Fatalf("operator profiles: %d", w.Code)
	}
	w = call(api, "itest-as", "admin", "POST", "/v1/ai/profiles", `{"name":"Fake A","base_url":"`+srv.URL+`","model":"fake-a","api_key":"sk-prof-AAA"}`)
	if w.Code != 200 || strings.Contains(w.Body.String(), "sk-prof-AAA") || !strings.Contains(w.Body.String(), `"Fake A"`) || !strings.Contains(w.Body.String(), `"builtin":true`) {
		t.Fatalf("create profile: %d %s", w.Code, w.Body.String())
	}
	var pid string
	pool.QueryRow(ctx, `SELECT id FROM ai_profiles WHERE tenant_id='itest-as' AND name='Fake A'`).Scan(&pid)
	if pid == "" {
		t.Fatal("profile not stored")
	}
	if w := call(api, "itest-as", "admin", "POST", "/v1/ai/profiles", `{"name":"fake a","base_url":"`+srv.URL+`","model":"x"}`); w.Code != 409 {
		t.Fatalf("duplicate name: %d", w.Code)
	}
	fm.script = []map[string]any{{"role": "assistant", "content": "ok"}}
	if w := call(api, "itest-as", "admin", "POST", "/v1/ai/profiles/"+pid+"/test", ""); !strings.Contains(w.Body.String(), `"ok":true`) || fm.auth != "Bearer sk-prof-AAA" {
		t.Fatalf("profile test: %s auth=%q", w.Body.String(), fm.auth)
	}
	if w := call(api, "itest-as", "admin", "POST", "/v1/ai/profiles/"+pid+"/activate", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"active":true`) {
		t.Fatalf("activate: %d %s", w.Code, w.Body.String())
	}
	var am string
	pool.QueryRow(ctx, `SELECT model FROM ai_settings WHERE tenant_id='itest-as'`).Scan(&am)
	if am != "fake-a" {
		t.Fatalf("active model %q", am)
	}
	if w := call(api, "itest-as", "admin", "DELETE", "/v1/ai/profiles/"+pid, ""); w.Code != 409 {
		t.Fatalf("deleting the active profile: %d", w.Code)
	}
	if w := call(api, "itest-as", "admin", "POST", "/v1/ai/profiles/local/activate", ""); w.Code != 200 {
		t.Fatalf("activate local: %d", w.Code)
	}
	if w := call(api, "itest-as", "admin", "DELETE", "/v1/ai/profiles/"+pid, ""); w.Code != 200 || strings.Contains(w.Body.String(), "Fake A") {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	// restore the original connection for the rest of the test
	call(api, "itest-as", "admin", "PUT", "/v1/ai/settings", `{"enabled":true,"base_url":"`+srv.URL+`","model":"fake-1"}`)

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

	// typed mode: the model works through the registry; a read runs, a write is only proposed,
	// bad arguments never reach the API, and the user's own role still decides what confirm can do
	t.Setenv("AI_TOOL_MODE", "typed")
	pool.Exec(ctx, `UPDATE alerts SET status='open', acknowledged_by=NULL WHERE id='itest-as-a1'`)
	fm.mu.Lock()
	fm.requests = nil
	fm.script = []map[string]any{
		toolMsg("t1", "list_alerts", `{"status":"open"}`),
		toolMsg("t2", "acknowledge_alert", `{"alert_id":"itest-as-a1"}`),
		toolMsg("t3", "get_alert", `{"alert_id":"../users"}`),
		toolMsg("t4", "api_request", `{"method":"GET","path":"/v1/alerts"}`),
		{"role": "assistant", "content": "One open alert; acknowledging it is waiting for you."},
	}
	fm.mu.Unlock()
	_, o = chat("itest-as", "viewer", "ack the pump alert")
	tr, _ := json.Marshal(o["trace"])
	if !strings.Contains(string(tr), `"tool":"list_alerts"`) || !strings.Contains(string(tr), "[READ]") || !strings.Contains(string(tr), "[LOW_RISK_WRITE]") ||
		strings.Count(string(tr), `"refused"`) != 2 {
		t.Fatalf("typed trace: %s", tr)
	}
	pend, _ := o["pending"].([]any)
	if len(pend) != 1 || !strings.Contains(pend[0].(map[string]any)["summary"].(string), "Acknowledge alert itest-as-a1") {
		t.Fatalf("typed pending (impact statement): %v", o["pending"])
	}
	pool.QueryRow(ctx, `SELECT status FROM alerts WHERE id='itest-as-a1'`).Scan(&st)
	if st != "open" {
		t.Fatalf("a proposal must not change anything: %s", st)
	}
	id4 := pend[0].(map[string]any)["id"].(string)
	// a viewer cannot acknowledge alerts, so confirming the AI's proposal cannot either
	call(api, "itest-as", "viewer", "POST", "/v1/assistant/actions/"+id4+"/confirm", "")
	pool.QueryRow(ctx, `SELECT status FROM alerts WHERE id='itest-as-a1'`).Scan(&st)
	if st != "open" {
		t.Fatalf("the assistant must inherit the user's role: viewer acknowledged an alert (%s)", st)
	}

	// report download: the model asks for a file, the user gets a button, the model never sees content
	pool.Exec(ctx, `DELETE FROM reports WHERE id='itest-as-rep'`)
	pool.Exec(ctx, `INSERT INTO reports(id,tenant_id,name,definition,created_by) VALUES('itest-as-rep','itest-as','Daily temps','{"metrics":[{"device_id":"itest-as-dev","point_id":"temp"}],"window_hours":24,"group_by":"hour"}'::jsonb,'test-user')`)
	fm.mu.Lock()
	fm.requests = nil
	fm.script = []map[string]any{
		toolMsg("r1", "list_reports", `{}`),
		toolMsg("r2", "offer_report_download", `{"report_id":"itest-as-rep","format":"csv"}`),
		toolMsg("r3", "offer_report_download", `{"report_id":"../users","format":"csv"}`),
		toolMsg("r4", "offer_report_download", `{"report_id":"itest-as-rep","format":"exe"}`),
		toolMsg("r5", "offer_report_download", `{"report_id":"does-not-exist","format":"csv"}`),
		{"role": "assistant", "content": "The CSV is ready."},
	}
	fm.mu.Unlock()
	_, o = chat("itest-as", "viewer", "give me the daily temps report as csv")
	files, _ := o["files"].([]any)
	if len(files) != 1 || files[0].(map[string]any)["path"] != "/v1/reports/itest-as-rep/download?format=csv" {
		t.Fatalf("download offer: %v", o)
	}
	tr, _ = json.Marshal(o["trace"])
	if strings.Count(string(tr), `"refused"`) != 2 || !strings.Contains(string(tr), "offered as a download button") || !strings.Contains(string(tr), "not available (404)") {
		t.Fatalf("report trace: %s", tr)
	}
	// the admin activity view is built from the audit trail: refused calls are visible, other
	// tenants and non-admins see nothing
	if w := call(api, "itest-as", "operator", "GET", "/v1/ai/activity", ""); w.Code != 403 {
		t.Fatalf("activity must be admin only: %d", w.Code)
	}
	w = call(api, "itest-as", "admin", "GET", "/v1/ai/activity", "")
	var act map[string]any
	json.Unmarshal(w.Body.Bytes(), &act)
	if w.Code != 200 || act["runs"].(float64) < 3 || act["refused_tool_calls"].(float64) < 2 || act["changes_confirmed"].(float64) < 1 || act["changes_rejected"].(float64) < 1 {
		t.Fatalf("activity: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "acknowledge_alert") || strings.Contains(w.Body.String(), "sk-secret") {
		t.Fatalf("activity should list tools used and never secrets: %s", w.Body.String())
	}
	w = call(api, "itest-as2", "admin", "GET", "/v1/ai/activity", "")
	json.Unmarshal(w.Body.Bytes(), &act)
	if act["runs"].(float64) != 0 || len(act["recent"].([]any)) != 0 {
		t.Fatalf("another tenant's activity leaked: %s", w.Body.String())
	}
}
