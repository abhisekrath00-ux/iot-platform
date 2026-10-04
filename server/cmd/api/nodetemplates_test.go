package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestIntegrationNodeTemplates(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-nt1")
	seed(t, s, "itest-nt2")
	ctx := t.Context()
	pool := s.st.Pool
	clean := func() {
		pool.Exec(ctx, `DELETE FROM node_templates WHERE tenant_id IN ('itest-nt1','itest-nt2')`)
		pool.Exec(ctx, `DELETE FROM tenant_features WHERE tenant_id IN ('itest-nt1','itest-nt2')`)
		pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id IN ('itest-nt1','itest-nt2')`)
		pool.Exec(ctx, `DELETE FROM users WHERE id IN ('nt-admin','nt-op','nt-admin2')`)
	}
	clean()
	t.Cleanup(clean)
	for _, u := range [][3]string{{"nt-admin", "itest-nt1", "admin"}, {"nt-op", "itest-nt1", "operator"}, {"nt-admin2", "itest-nt2", "admin"}} {
		pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES($1,$2,$1||'@nt-test.example','U',$3)`, u[0], u[1], u[2])
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/node-templates", s.listNodeTemplates)
	mux.HandleFunc("POST /v1/node-templates", s.createNodeTemplate)
	mux.HandleFunc("PUT /v1/node-templates/{id}", s.updateNodeTemplate)
	mux.HandleFunc("DELETE /v1/node-templates/{id}", s.deleteNodeTemplate)
	h := s.activeUser(mux)
	as := func(tenant, user, role, method, path, body string) (int, string) {
		w := callAs(h, tenant, user, role, method, path, body)
		return w.Code, w.Body.String()
	}
	const good = `{"name":"Celsius to Fahrenheit","description":"msg.payload * 9/5 + 32","code":"msg.payload = msg.payload * 9 / 5 + 32; return msg;"}`
	// off by default: the tenant has not enabled function nodes
	if c, _ := as("itest-nt1", "nt-admin", "admin", "POST", "/v1/node-templates", good); c != 403 {
		t.Fatalf("feature off = %d, want 403", c)
	}
	pool.Exec(ctx, `INSERT INTO tenant_features(tenant_id,feature,enabled) VALUES('itest-nt1','function_nodes',true) ON CONFLICT (tenant_id,feature) DO UPDATE SET enabled=true`)
	if c, b := as("itest-nt1", "nt-admin", "admin", "POST", "/v1/node-templates", good); c != 201 {
		t.Fatalf("create = %d %s", c, b)
	}
	// only admins: an operator is refused even with the feature on
	if c, _ := as("itest-nt1", "nt-op", "operator", "POST", "/v1/node-templates", strings.Replace(good, "Celsius", "Other", 1)); c != 403 {
		t.Fatalf("operator create = %d", c)
	}
	if c, _ := as("itest-nt1", "nt-op", "operator", "GET", "/v1/node-templates", ""); c != 403 {
		t.Fatalf("operator list = %d", c)
	}
	// the same code checks as a function node: syntax errors are refused, as are blank, huge and bad names
	for _, bad := range []string{
		`{"name":"Broken","code":"msg.payload = ;"}`,
		`{"name":"","code":"return msg;"}`,
		`{"name":"x","code":""}`,
		`{"name":"` + strings.Repeat("n", 61) + `","code":"return msg;"}`,
		`{"name":"x","code":"` + strings.Repeat("a", 4001) + `"}`,
		`{"name":"x","description":"` + strings.Repeat("d", 301) + `","code":"return msg;"}`,
	} {
		if c, _ := as("itest-nt1", "nt-admin", "admin", "POST", "/v1/node-templates", bad); c != 400 {
			t.Errorf("bad template accepted (%d): %.60s", c, bad)
		}
	}
	// names are unique ignoring case
	if c, _ := as("itest-nt1", "nt-admin", "admin", "POST", "/v1/node-templates", strings.Replace(good, "Celsius to Fahrenheit", "celsius TO fahrenheit", 1)); c != 409 {
		t.Fatalf("duplicate name = %d", c)
	}
	c, body := as("itest-nt1", "nt-admin", "admin", "GET", "/v1/node-templates", "")
	if c != 200 || !strings.Contains(body, "Celsius to Fahrenheit") {
		t.Fatalf("list: %d %s", c, body)
	}
	var id string
	pool.QueryRow(ctx, `SELECT id FROM node_templates WHERE tenant_id='itest-nt1'`).Scan(&id)
	// another tenant sees nothing and can change nothing, even with the feature on
	pool.Exec(ctx, `INSERT INTO tenant_features(tenant_id,feature,enabled) VALUES('itest-nt2','function_nodes',true) ON CONFLICT (tenant_id,feature) DO UPDATE SET enabled=true`)
	if c, b := as("itest-nt2", "nt-admin2", "admin", "GET", "/v1/node-templates", ""); c != 200 || strings.Contains(b, "Celsius") {
		t.Fatalf("cross-tenant list: %d %s", c, b)
	}
	if c, _ := as("itest-nt2", "nt-admin2", "admin", "PUT", "/v1/node-templates/"+id, good); c != 404 {
		t.Fatalf("cross-tenant update = %d", c)
	}
	if c, _ := as("itest-nt2", "nt-admin2", "admin", "DELETE", "/v1/node-templates/"+id, ""); c != 404 {
		t.Fatalf("cross-tenant delete = %d", c)
	}
	var still int
	pool.QueryRow(ctx, `SELECT count(*) FROM node_templates WHERE id=$1`, id).Scan(&still)
	if still != 1 {
		t.Fatal("another tenant deleted the template")
	}
	// update and delete by the owner
	if c, b := as("itest-nt1", "nt-admin", "admin", "PUT", "/v1/node-templates/"+id, `{"name":"C to F","code":"msg.payload = msg.payload + 1; return msg;"}`); c != 204 {
		t.Fatalf("update = %d %s", c, b)
	}
	// turning the feature off locks the templates again
	pool.Exec(ctx, `UPDATE tenant_features SET enabled=false WHERE tenant_id='itest-nt1'`)
	if c, _ := as("itest-nt1", "nt-admin", "admin", "GET", "/v1/node-templates", ""); c != 403 {
		t.Fatalf("list with feature off = %d", c)
	}
	pool.Exec(ctx, `UPDATE tenant_features SET enabled=true WHERE tenant_id='itest-nt1'`)
	if c, _ := as("itest-nt1", "nt-admin", "admin", "DELETE", "/v1/node-templates/"+id, ""); c != 204 {
		t.Fatalf("delete = %d", c)
	}
	var audits int
	pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id='itest-nt1' AND action LIKE 'node_template.%'`).Scan(&audits)
	if audits != 3 {
		t.Fatalf("audit rows = %d, want create+update+delete", audits)
	}
}
