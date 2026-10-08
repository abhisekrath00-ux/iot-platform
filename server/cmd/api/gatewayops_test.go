package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestIntegrationGatewayOps(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-go1")
	pool := s.st.Pool
	clean := func() {
		pool.Exec(t.Context(), `DELETE FROM gateway_ops WHERE tenant_id='itest-go1'`)
		pool.Exec(t.Context(), `DELETE FROM audit_log WHERE tenant_id='itest-go1'`)
	}
	clean()
	t.Cleanup(clean)
	for _, u := range [][2]string{{"go-a", "admin"}, {"go-b", "admin"}, {"go-v", "viewer"}} {
		pool.Exec(t.Context(), `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES($1,'itest-go1',$1||'@go-test.example','x',$2) ON CONFLICT DO NOTHING`, u[0], u[1])
	}
	t.Cleanup(func() { pool.Exec(t.Context(), `DELETE FROM users WHERE id IN ('go-a','go-b','go-v')`) })
	type pub struct {
		topic   string
		payload map[string]any
	}
	var sent []pub
	s.opsPublishHook = func(topic string, b []byte) error {
		m := map[string]any{}
		json.Unmarshal(b, &m)
		sent = append(sent, pub{topic, m})
		return nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/gateways/{id}/ops", s.requestGatewayOp)
	mux.HandleFunc("GET /v1/gateways/{id}/ops", s.listGatewayOps)
	mux.HandleFunc("POST /v1/gateway-ops/{id}/approve", s.approveGatewayOp)
	h := s.activeUser(mux)
	do := func(user, role, method, path, body string) (int, string) {
		w := callAs(h, "itest-go1", user, role, method, path, body)
		return w.Code, w.Body.String()
	}
	gw := "/v1/gateways/itest-go1-gw/ops"

	// only admins; bad input refused; unknown gateway 404
	if c, _ := do("go-v", "viewer", "POST", gw, `{"kind":"logs"}`); c != 403 {
		t.Fatalf("viewer = %d", c)
	}
	if c, _ := do("go-a", "admin", "POST", gw, `{"kind":"reboot"}`); c != 400 {
		t.Fatalf("bad kind = %d", c)
	}
	if c, _ := do("go-a", "admin", "POST", gw, `{"kind":"logs","lines":9999}`); c != 400 {
		t.Fatalf("bad lines = %d", c)
	}
	if c, _ := do("go-a", "admin", "POST", "/v1/gateways/nope/ops", `{"kind":"logs"}`); c != 404 {
		t.Fatalf("unknown gateway = %d", c)
	}

	// logs: published at once to the gateway's own topic; a second request while one is open is refused
	c, body := do("go-a", "admin", "POST", gw, `{"kind":"logs","lines":50}`)
	if c != 202 || len(sent) != 1 || sent[0].topic != "t/itest-go1/g/itest-go1-gw/ops" || sent[0].payload["kind"] != "logs" || sent[0].payload["lines"] != 50.0 {
		t.Fatalf("logs: %d %s %+v", c, body, sent)
	}
	if _, has := sent[0].payload["approved_by"]; has {
		t.Fatal("a logs request must not carry an approver")
	}
	if c, _ := do("go-a", "admin", "POST", gw, `{"kind":"logs"}`); c != 409 {
		t.Fatalf("second open op = %d", c)
	}
	// the gateway's answer (what ingest stores) closes it
	var logID string
	pool.QueryRow(t.Context(), `SELECT id FROM gateway_ops WHERE tenant_id='itest-go1' AND kind='logs'`).Scan(&logID)
	pool.Exec(t.Context(), `UPDATE gateway_ops SET status='done', result='{"ok":true,"lines":["a","b"]}' WHERE id=$1`, logID)

	// restart: nothing is sent until a different person approves
	sent = nil
	c, body = do("go-a", "admin", "POST", gw, `{"kind":"restart"}`)
	if c != 202 || !strings.Contains(body, "pending_approval") || len(sent) != 0 {
		t.Fatalf("restart request: %d %s sent=%d", c, body, len(sent))
	}
	var rid string
	pool.QueryRow(t.Context(), `SELECT id FROM gateway_ops WHERE tenant_id='itest-go1' AND kind='restart'`).Scan(&rid)
	ap := "/v1/gateway-ops/" + rid + "/approve"
	if c, _ := do("go-a", "admin", "POST", ap, ``); c != 409 || len(sent) != 0 {
		t.Fatalf("self approval = %d", c)
	}
	if c, _ := do("go-v", "viewer", "POST", ap, ``); c != 403 || len(sent) != 0 {
		t.Fatalf("viewer approval = %d", c)
	}
	if c, b := do("go-b", "admin", "POST", ap, ``); c != 200 || len(sent) != 1 || sent[0].payload["kind"] != "restart" || sent[0].payload["approved_by"] != "go-b" {
		t.Fatalf("approval: %d %s %+v", c, b, sent)
	}
	if c, _ := do("go-b", "admin", "POST", ap, ``); c != 409 || len(sent) != 1 {
		t.Fatalf("second approval = %d", c)
	}
	// list shows both, newest first, and an old open request reads as expired
	pool.Exec(t.Context(), `INSERT INTO gateway_ops(id,tenant_id,gateway_id,kind,status,requested_by,created_at) VALUES('go-old','itest-go1','itest-go1-gw','restart','pending_approval','go-a', now()-interval '20 minutes')`)
	c, body = do("go-a", "admin", "GET", gw, "")
	if c != 200 || !strings.Contains(body, `"expired"`) || !strings.Contains(body, rid) || !strings.Contains(body, `"approved_by":"go-b"`) {
		t.Fatalf("list: %d %s", c, body)
	}
	// an expired request cannot be approved
	if c, _ := do("go-b", "admin", "POST", "/v1/gateway-ops/go-old/approve", ``); c != 409 {
		t.Fatalf("expired approval = %d", c)
	}
	// approving again after the decision stays refused
	if c, _ := do("go-b", "admin", "POST", "/v1/gateway-ops/"+rid+"/approve", ``); c != 409 {
		t.Fatalf("repeat = %d", c)
	}
}
