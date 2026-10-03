package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
)

func TestIntegrationControlNodeEndToEnd(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-cn")
	seed(t, s, "itest-cn2")
	ctx := context.Background()
	pool := s.st.Pool
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, a...); err != nil {
			t.Fatalf("%q: %v", q, err)
		}
	}
	exec(`DELETE FROM commands WHERE tenant_id IN ('itest-cn','itest-cn2')`)
	exec(`DELETE FROM control_targets WHERE tenant_id IN ('itest-cn','itest-cn2')`)
	exec(`DELETE FROM tenant_features WHERE tenant_id IN ('itest-cn','itest-cn2')`)
	exec(`INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('itest-cn-adm','itest-cn','itest-cn-adm@example.test','A','admin') ON CONFLICT DO NOTHING`)
	exec(`INSERT INTO control_targets(id,tenant_id,name,kind,gateway_id,device_id,point_id,min_value,max_value,max_per_hour,enabled,created_by)
	      VALUES('itest-cn-t1','itest-cn','Setpoint','modbus_write','itest-cn-gw','itest-cn-dev','temp',10,30,2,true,'itest-cn-adm'),
	            ('itest-cn-off','itest-cn','Off one','modbus_write','itest-cn-gw','itest-cn-dev','temp',0,1,5,false,'itest-cn-adm')`)
	exec(`INSERT INTO control_targets(id,tenant_id,name,kind,gateway_id,device_id,point_id,allowed_values,max_per_hour,approval_mode,enabled,created_by)
	      VALUES('itest-cn-siren','itest-cn','Siren','alarm_output','itest-cn-gw','itest-cn-dev','temp','{0,1}',5,'automatic',true,'itest-cn-adm')`)
	mk := func(flowID string) *flow.PGControl {
		return &flow.PGControl{Pool: pool, Tenant: "itest-cn", FlowID: flowID, FlowName: "Hot room", TrigDevice: "itest-cn-dev", TrigPoint: "temp", TrigValue: 42}
	}
	req := func(c *flow.PGControl, target string, v float64) (string, string, error) {
		return c.RequestControl(ctx, target, v)
	}
	// feature off: nothing is raised
	if _, _, err := req(mk("f1"), "itest-cn-t1", 20); err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("feature off: %v", err)
	}
	exec(`INSERT INTO tenant_features(tenant_id,feature,enabled,updated_by) VALUES('itest-cn','control_nodes',true,'itest-cn-adm')`)
	if _, _, err := req(mk("f1"), "itest-cn-t1", 99); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("out of range must be refused, not clamped: %v", err)
	}
	if _, _, err := req(mk("f1"), "itest-cn-off", 1); err == nil || !strings.Contains(err.Error(), "switched off") {
		t.Fatalf("disabled target: %v", err)
	}
	if _, _, err := (&flow.PGControl{Pool: pool, Tenant: "itest-cn2", FlowID: "f9"}).RequestControl(ctx, "itest-cn-t1", 20); err == nil {
		t.Fatal("another tenant's target must not be found")
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM commands WHERE tenant_id IN ('itest-cn','itest-cn2')`).Scan(&n)
	if n != 0 {
		t.Fatalf("refusals must create no command, found %d", n)
	}
	id, note, err := req(mk("f1"), "itest-cn-t1", 20)
	if err != nil || note != "" {
		t.Fatalf("valid request: %v %q", err, note)
	}
	var status, by, action, params, reason string
	pool.QueryRow(ctx, `SELECT status, requested_by, action, parameters::text, reason FROM commands WHERE request_id=$1`, id).Scan(&status, &by, &action, &params, &reason)
	if status != "pending_approval" || by != "flow-engine:itest-cn" || action != "modbus.write" || !strings.Contains(params, `"value": 20`) || !strings.Contains(reason, "Hot room") || !strings.Contains(reason, "42") {
		t.Fatalf("command: %s %s %s %s %s", status, by, action, params, reason)
	}
	var audits int
	pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id='itest-cn' AND action='control.request' AND target=$1 AND actor='flow:f1'`, id).Scan(&audits)
	if audits != 1 {
		t.Fatalf("audit rows: %d", audits)
	}
	// automatic target: still only a request (the automatic executor is not built), and it says so
	_, note, err = req(mk("f1"), "itest-cn-siren", 1)
	if err != nil || !strings.Contains(note, "not built yet") {
		t.Fatalf("automatic target: %v %q", err, note)
	}
	// per-flow pending cap (3) and per-target rate limit (2 per hour)
	if _, _, err := req(mk("f1"), "itest-cn-t1", 21); err != nil {
		t.Fatalf("second request on the target: %v", err)
	}
	if _, _, err := req(mk("f1"), "itest-cn-t1", 22); err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("rate limit: %v", err)
	}
	if _, _, err := req(mk("f1"), "itest-cn-siren", 0); err == nil || !strings.Contains(err.Error(), "waiting for approval") {
		t.Fatalf("pending cap: %v", err)
	}

	// approval: the flow's service user can never approve; a person can
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/commands/{id}/approve", s.approveCommand)
	api.HandleFunc("GET /v1/commands", s.listCommands)
	api.HandleFunc("PUT /v1/features/{feature}", s.putFeature)
	api.HandleFunc("POST /v1/flows/graph/test", s.testFlowGraph)
	if w := call(api, "itest-cn", "viewer", "POST", "/v1/commands/"+id+"/approve", ""); w.Code != 403 {
		t.Fatalf("viewer approved: %d", w.Code)
	}
	if w := call(api, "itest-cn", "operator", "GET", "/v1/commands", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "Hot room") {
		t.Fatalf("approver must see why: %d %s", w.Code, w.Body.String())
	}
	if w := call(api, "itest-cn", "operator", "POST", "/v1/commands/"+id+"/approve", ""); w.Code != 200 {
		t.Fatalf("operator approve: %d %s", w.Code, w.Body.String())
	}
	var approver string
	pool.QueryRow(ctx, `SELECT approved_by FROM commands WHERE request_id=$1`, id).Scan(&approver)
	if approver == by {
		t.Fatal("requester and approver must differ")
	}

	// saving a flow with a control node: an admin and the tenant feature
	exec(`DELETE FROM notification_channels WHERE tenant_id IN ('itest-cn','itest-cn2')`)
	exec(`INSERT INTO notification_channels(id,tenant_id,type,target) VALUES('itest-cn-ch','itest-cn','email','boss@example.com'),('itest-cn2-ch','itest-cn2','email','boss@example.com')`)
	nodes := func(ch string) string {
		return `{"graph":{"nodes":[{"id":"t","type":"trigger","device_id":"d","point_id":"p","op":">","value":1},
	  {"id":"c","type":"control","target_id":"itest-cn-t1","value":20},
	  {"id":"n","type":"notify","channel_id":"` + ch + `","message":"asked"},{"id":"f","type":"notify","channel_id":"` + ch + `","message":"refused"}],
	  "edges":[{"from":"t","to":"c"},{"from":"c","port":"0","to":"n"},{"from":"c","port":"1","to":"f"}]}}`
	}
	api.HandleFunc("POST /v1/flows", s.createFlow)
	if w := call(api, "itest-cn", "operator", "POST", "/v1/flows", `{"name":"x","definition":`+nodes("itest-cn-ch")+`}`); w.Code != 403 {
		t.Fatalf("operator saved a control node: %d %s", w.Code, w.Body.String())
	}
	if w := call(api, "itest-cn2", "admin", "POST", "/v1/flows", `{"name":"x","definition":`+nodes("itest-cn2-ch")+`}`); w.Code != 403 {
		t.Fatalf("admin without the feature: %d %s", w.Code, w.Body.String())
	}
	if w := call(api, "itest-cn", "admin", "POST", "/v1/flows?draft=1", `{"name":"Hot room","definition":`+nodes("itest-cn-ch")+`}`); w.Code != 201 {
		t.Fatalf("admin with the feature should save: %d %s", w.Code, w.Body.String())
	}
	// a dry run raises nothing, whoever runs it
	before := 0
	pool.QueryRow(ctx, `SELECT count(*) FROM commands WHERE tenant_id='itest-cn'`).Scan(&before)
	dry := `{"definition":` + nodes("itest-cn-ch") + `,"value":5}`
	w := call(api, "itest-cn", "operator", "POST", "/v1/flows/graph/test", dry)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "dry run") {
		t.Fatalf("dry run: %d %s", w.Code, w.Body.String())
	}
	after := 0
	pool.QueryRow(ctx, `SELECT count(*) FROM commands WHERE tenant_id='itest-cn'`).Scan(&after)
	if after != before {
		t.Fatalf("a dry run created a command: %d -> %d", before, after)
	}
}
