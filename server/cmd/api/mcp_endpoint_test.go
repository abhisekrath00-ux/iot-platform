package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestIntegrationMCPEndpoint(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-mcp")
	pool := s.st.Pool
	ctx := t.Context()
	pool.Exec(ctx, `DELETE FROM assistant_actions WHERE tenant_id='itest-mcp'`)
	pool.Exec(ctx, `DELETE FROM sites WHERE tenant_id='itest-mcp' AND name='MCP Plant'`)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM assistant_actions WHERE tenant_id='itest-mcp'`)
		pool.Exec(ctx, `DELETE FROM sites WHERE tenant_id='itest-mcp' AND name='MCP Plant'`)
	})
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/alerts", s.listAlerts)
	api.HandleFunc("POST /v1/sites", s.createSite)
	api.HandleFunc("POST /v1/mcp", s.mcpHandler)
	api.HandleFunc("POST /v1/assistant/actions/{id}/confirm", s.confirmAssistantAction)
	s.inner = api
	rpc := func(role, body string) map[string]any {
		w := call(api, "itest-mcp", role, "POST", "/v1/mcp", body)
		var o map[string]any
		json.Unmarshal(w.Body.Bytes(), &o)
		return o
	}
	o := rpc("admin", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if o["result"] == nil {
		t.Fatalf("initialize: %v", o)
	}
	l, _ := json.Marshal(rpc("admin", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	for _, want := range []string{`"list_alerts"`, `"create_site"`, `"inputSchema"`} {
		if !strings.Contains(string(l), want) {
			t.Errorf("tools/list lacks %s", want)
		}
	}
	for _, bad := range []string{"offer_report_download", "approve", "api_request", "user"} {
		if strings.Contains(string(l), `"name":"`+bad) {
			t.Errorf("tools/list must not expose %s", bad)
		}
	}
	// a read runs
	r, _ := json.Marshal(rpc("viewer", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_alerts","arguments":{"status":"open"}}}`))
	if strings.Contains(string(r), `"isError":true`) || !strings.Contains(string(r), `"content"`) {
		t.Fatalf("read: %s", r)
	}
	// a write is only proposed; nothing is created
	w, _ := json.Marshal(rpc("admin", `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"create_site","arguments":{"name":"MCP Plant"}}}`))
	if !strings.Contains(string(w), "PROPOSED, NOT DONE") {
		t.Fatalf("write: %s", w)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM sites WHERE tenant_id='itest-mcp' AND name='MCP Plant'`).Scan(&n)
	if n != 0 {
		t.Fatal("MCP must not execute a change")
	}
	// bad arguments, unknown tools and unknown methods are refused cleanly
	b, _ := json.Marshal(rpc("admin", `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"get_alert","arguments":{"alert_id":"../users"}}}`))
	if !strings.Contains(string(b), `"isError":true`) {
		t.Errorf("bad id: %s", b)
	}
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"approve_command","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"resources/list"}`,
	} {
		e, _ := json.Marshal(rpc("admin", body))
		if !strings.Contains(string(e), `"error"`) {
			t.Errorf("expected error: %s", e)
		}
	}
	var audits int
	pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id='itest-mcp' AND action='mcp.call'`).Scan(&audits)
	if audits < 3 {
		t.Errorf("mcp.call audited %d times", audits)
	}
}
