package main

// Offline eval layer: JSON-RPC plumbing, tool allowlist, and error paths.
// These run without a database; DB-backed answer checks live in cmd/mcpeval
// against a seeded stack.

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func call(t *testing.T, body string) rpcResp {
	t.Helper()
	s := &server{} // DB untouched on these paths
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handle(rec, req)
	var resp rpcResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response not json-rpc: %v (%s)", err, rec.Body.String())
	}
	return resp
}

func TestInitializeAdvertisesProtocol(t *testing.T) {
	r := call(t, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	res, ok := r.Result.(map[string]any)
	if !ok || res["protocolVersion"] == "" {
		t.Fatalf("bad initialize: %+v", r)
	}
}

func TestToolsListIsReadOnlyAllowlist(t *testing.T) {
	r := call(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	res := r.Result.(map[string]any)
	list, _ := res["tools"].([]any)
	if len(list) != 7 {
		t.Fatalf("want 7 tools, got %d", len(list))
	}
	for _, tool := range list {
		name := tool.(map[string]any)["name"].(string)
		for _, banned := range []string{"write", "set", "command", "actuate", "delete", "update"} {
			if strings.Contains(name, banned) {
				t.Fatalf("write-capable tool exposed: %s", name)
			}
		}
	}
}

func TestUnknownToolErrors(t *testing.T) {
	r := call(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"drop_table","arguments":{}}}`)
	if r.Error == nil {
		t.Fatal("unknown tool accepted")
	}
}

func TestUnknownMethodErrors(t *testing.T) {
	r := call(t, `{"jsonrpc":"2.0","id":4,"method":"resources/read"}`)
	errMap, _ := r.Error.(map[string]any)
	if errMap["code"].(float64) != -32601 {
		t.Fatalf("want -32601, got %+v", r.Error)
	}
}

func TestNotificationAcked(t *testing.T) {
	s := &server{}
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	rec := httptest.NewRecorder()
	s.handle(rec, req)
	if rec.Code != 200 {
		t.Fatalf("notification status %d", rec.Code)
	}
}

func TestClampInt(t *testing.T) {
	cases := []struct {
		in             any
		def, lo, hi, w int
	}{{nil, 5, 1, 10, 5}, {float64(99), 5, 1, 10, 10}, {float64(0), 5, 1, 10, 1}, {"x", 5, 1, 10, 5}, {float64(7), 5, 1, 10, 7}}
	for _, c := range cases {
		if g := clampInt(c.in, c.def, c.lo, c.hi); g != c.w {
			t.Fatalf("clampInt(%v)=%d want %d", c.in, g, c.w)
		}
	}
}
