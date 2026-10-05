package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/aitools"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/llm"
)

// POST /v1/mcp is a Model Context Protocol endpoint (JSON-RPC 2.0 over HTTP) that exposes the same
// typed tools as the in-app assistant. It adds no powers:
//   - authentication is the normal API auth (user token or API key); the tool runs as that
//     principal through the real handler chain, so roles, tenant and customer scope apply;
//   - reads run at once; a write is only PROPOSED and returned as a pending action that a signed-in
//     person must confirm in the app (an API-key principal can never confirm, so it is read-only);
//   - approving commands, users, keys, secrets and settings are not tools, so they are not here;
//   - every call is audited ("mcp.call") and rate-limited.
//
// Report downloads are not offered over MCP (they return files, not text).
var mcpLimiter = struct {
	sync.Mutex
	m map[string][]time.Time
}{m: map[string][]time.Time{}}

func allowMCP(key string) bool {
	mcpLimiter.Lock()
	defer mcpLimiter.Unlock()
	cut := time.Now().Add(-time.Minute)
	keep := mcpLimiter.m[key][:0]
	for _, t := range mcpLimiter.m[key] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= 120 {
		mcpLimiter.m[key] = keep
		return false
	}
	mcpLimiter.m[key] = append(keep, time.Now())
	return true
}

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func rpcErr(id json.RawMessage, code int, msg string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}}
}

func (s *server) mcpHandler(w http.ResponseWriter, r *http.Request) {
	tenant, user, role := auth.Tenant(r), auth.User(r), auth.Role(r)
	if !allowMCP(tenant + "|" + user) {
		http.Error(w, "too many MCP calls", 429)
		return
	}
	var req rpcReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&req); err != nil || req.JSONRPC != "2.0" {
		writeJSON(w, 400, rpcErr(nil, -32700, "send one JSON-RPC 2.0 request"))
		return
	}
	if len(req.ID) == 0 { // notification
		w.WriteHeader(http.StatusAccepted)
		return
	}
	switch req.Method {
	case "initialize":
		writeJSON(w, 200, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
			"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo":   map[string]any{"name": "hexthings", "version": "1"},
			"instructions": "Typed HexThings tools. Reads run now. Changes are proposed only; a person confirms them in the app.",
		}})
	case "ping":
		writeJSON(w, 200, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
	case "tools/list":
		var tools []map[string]any
		for _, t := range llm.ToolsForMCP(aitools.Specs()) {
			if t["name"] == "offer_report_download" {
				continue
			}
			tools = append(tools, t)
		}
		writeJSON(w, 200, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"tools": tools}})
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if json.Unmarshal(req.Params, &p) != nil || p.Name == "" || p.Name == "offer_report_download" {
			writeJSON(w, 200, rpcErr(req.ID, -32602, "unknown tool or bad params"))
			return
		}
		if _, ok := aitools.Lookup(p.Name); !ok {
			writeJSON(w, 200, rpcErr(req.ID, -32602, "unknown tool"))
			return
		}
		args, _ := json.Marshal(p.Arguments)
		var tc llm.ToolCall
		tc.ID, tc.Type = "mcp", "function"
		tc.Func.Name, tc.Func.Arguments = p.Name, string(args)
		var trace []traceStep
		var plan []string
		var pending []pendingAction
		var files []fileOffer
		rc := runCtx{via: "mcp", typedOnly: true, files: &files}
		out := s.runTypedTool(r.Context(), tenant, user, role, rc, tc, &trace, &plan, &pending)
		isErr := false
		if len(trace) > 0 && trace[len(trace)-1].Status != "ok" && trace[len(trace)-1].Status != "proposed" {
			isErr = true
		}
		text := out
		if len(pending) > 0 {
			var b strings.Builder
			b.WriteString("PROPOSED, NOT DONE. A signed-in person must confirm in HexThings (assistant panel):\n")
			for _, pa := range pending {
				fmt.Fprintf(&b, "- %s (%s %s)\n", pa.Summary, pa.Method, pa.Path)
			}
			text = b.String()
		}
		s.audit(r, "mcp.call", user, map[string]any{"tool": p.Name, "proposed": len(pending), "error": isErr, "via_key": auth.ViaKey(r)})
		writeJSON(w, 200, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
			"content": []map[string]any{{"type": "text", "text": truncStr(text, 12000)}}, "isError": isErr}})
	default:
		writeJSON(w, 200, rpcErr(req.ID, -32601, "method not found"))
	}
}
