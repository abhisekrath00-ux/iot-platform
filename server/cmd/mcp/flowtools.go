package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
	"github.com/google/uuid"
	"net/http"
)

// Text-to-flow: the MCP client (an LLM the caller already uses) turns the
// user's words into a graph; this server owns the schema, the validation and
// the safety rules. Nothing here deploys: draft_flow_graph stores an
// UNPUBLISHED draft that a person must review and publish in the UI.

var flowTools = []map[string]any{
	{"name": "describe_flow_nodes", "description": "Catalogue of flow graph node types, their fields, output ports, and the rules a graph must satisfy. Call this before building a graph.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
	{"name": "validate_flow_graph", "description": "Validate a flow graph and dry-run it against one reading. Nothing is saved or sent.",
		"inputSchema": map[string]any{"type": "object", "required": []string{"graph"}, "properties": map[string]any{
			"graph": map[string]any{"type": "object", "description": "{nodes:[...], edges:[...]} as described by describe_flow_nodes"},
			"value": map[string]any{"type": "number", "description": "reading to dry-run; default: the trigger threshold plus one"}}}},
	{"name": "draft_flow_graph", "description": "Save a flow graph as an unpublished DRAFT for a person to review and publish. Needs an operator or admin token. Never publishes or enables anything. function nodes are not accepted here.",
		"inputSchema": map[string]any{"type": "object", "required": []string{"name", "graph"}, "properties": map[string]any{
			"name":  map[string]any{"type": "string", "maxLength": 128},
			"graph": map[string]any{"type": "object"}}}},
}

var nodeCatalogue = map[string]any{
	"rules": []string{
		"exactly one trigger node; it has no inputs",
		"notify and debug nodes are terminal (no outputs)",
		"no cycles; every node must be reachable from the trigger; at least one notify node",
		"at most 50 nodes and 150 edges; edge {from, port, to}, port defaults to \"0\"",
		"message templates may use {value}, {device_id}, {point_id}, {vars.name}",
		"function nodes cannot be created through MCP",
	},
	"nodes": []map[string]any{
		{"type": "trigger", "fields": "device_id, point_id, op (> < >= <= == !=), value", "ports": "0"},
		{"type": "switch", "fields": "property (value|device_id|point_id|vars.x), mode (first|all), rules[{op (== != > < >= <= contains else), value}]", "ports": "one per rule, \"0\"..\"n-1\""},
		{"type": "change", "fields": "changes[{action (set|delete|move|add|mul), property, value, to}]; properties: value or vars.name", "ports": "0"},
		{"type": "condition", "fields": "op, value (compares msg.value)", "ports": "0 (passes when true)"},
		{"type": "delay", "fields": "seconds (1-3600)", "ports": "0"},
		{"type": "template", "fields": "template (max 500), target (variable name)", "ports": "0"},
		{"type": "range", "fields": "in_min, in_max (differ), out_min, out_max, clamp", "ports": "0"},
		{"type": "debug", "fields": "message", "ports": "none"},
		{"type": "notify", "fields": "channel_id (an existing notification channel), message", "ports": "none"},
	},
}

func (s *server) flowTool(r *http.Request, name string, args map[string]any) (any, bool, error) {
	switch name {
	case "describe_flow_nodes":
		return nodeCatalogue, true, nil
	case "validate_flow_graph":
		g, err := graphArg(args)
		if err != nil {
			return nil, true, err
		}
		d := flow.Definition{Graph: g}
		if d.HasFunctionNodes() {
			return nil, true, fmt.Errorf("function nodes are not supported through MCP")
		}
		if err := flow.Validate(d); err != nil {
			return map[string]any{"valid": false, "error": err.Error()}, true, nil
		}
		t := d.Trig()
		v := t.Value + 1
		if f, ok := args["value"].(float64); ok {
			v = f
		}
		res := d.Exec(v, t.DeviceID, t.PointID, flow.ExecOptions{})
		acts := []map[string]any{}
		for _, a := range res.Actions {
			acts = append(acts, map[string]any{"channel_id": a.ChannelID, "message": a.Message, "delay_seconds": int(a.Delay.Seconds())})
		}
		return map[string]any{"valid": true, "dry_run": map[string]any{"value": v, "matched": res.Matched, "actions": acts, "debug": res.Debug}}, true, nil
	case "draft_flow_graph":
		role := auth.Role(r)
		if role != "admin" && role != "operator" {
			return nil, true, fmt.Errorf("insufficient role: drafting a flow needs operator or admin")
		}
		fname, _ := args["name"].(string)
		fname = strings.TrimSpace(fname)
		if fname == "" || len(fname) > 128 || strings.ContainsAny(fname, "<>\x00") {
			return nil, true, fmt.Errorf("name invalid")
		}
		g, err := graphArg(args)
		if err != nil {
			return nil, true, err
		}
		d := flow.Definition{Graph: g}
		if d.HasFunctionNodes() {
			return nil, true, fmt.Errorf("function nodes are not supported through MCP")
		}
		if err := flow.Validate(d); err != nil {
			return nil, true, err
		}
		tenant := auth.Tenant(r)
		for _, slot := range d.ChannelSlots() {
			var ok bool
			if s.st.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM notification_channels WHERE id=$1 AND tenant_id=$2)`, *slot, tenant).Scan(&ok) != nil || !ok {
				return nil, true, fmt.Errorf("notify channel %q not found for this tenant", *slot)
			}
		}
		def, _ := json.Marshal(d)
		id, ver := uuid.NewString(), uuid.NewString()
		tx, err := s.st.Pool.Begin(r.Context())
		if err != nil {
			return nil, true, fmt.Errorf("db")
		}
		defer tx.Rollback(r.Context())
		if _, err := tx.Exec(r.Context(), `INSERT INTO flows(id,tenant_id,name,definition,created_by) VALUES($1,$2,$3,$4,$5)`,
			id, tenant, fname, def, auth.User(r)); err != nil {
			return nil, true, fmt.Errorf("db")
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO flow_versions(id,flow_id,tenant_id,version,definition,status,created_by)
			VALUES($1,$2,$3,1,$4,'draft',$5)`, ver, id, tenant, def, auth.User(r)); err != nil {
			return nil, true, fmt.Errorf("db")
		}
		ad, _ := json.Marshal(map[string]any{"name": fname, "via": "mcp"})
		if _, err := tx.Exec(r.Context(), `INSERT INTO audit_log(tenant_id,actor,action,target,detail) VALUES($1,$2,'flow.draft_mcp',$3,$4)`,
			tenant, auth.User(r), id, ad); err != nil {
			return nil, true, fmt.Errorf("db")
		}
		if err := tx.Commit(r.Context()); err != nil {
			return nil, true, fmt.Errorf("db")
		}
		return map[string]any{"flow_id": id, "version": 1, "status": "draft",
			"note": "Saved as an unpublished draft. It does not run until a person reviews and publishes it in the Flows page."}, true, nil
	}
	return nil, false, nil
}

func graphArg(args map[string]any) (*flow.Graph, error) {
	raw, ok := args["graph"]
	if !ok {
		return nil, fmt.Errorf("graph required")
	}
	b, err := json.Marshal(raw)
	if err != nil || len(b) > 256<<10 {
		return nil, fmt.Errorf("graph invalid or too large")
	}
	var g flow.Graph
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&g); err != nil {
		return nil, fmt.Errorf("graph shape: %v", err)
	}
	return &g, nil
}
