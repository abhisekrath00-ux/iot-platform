package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestIntegrationScopedDashboards(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-ds1")
	seed(t, s, "itest-ds2")
	ctx := context.Background()
	pool := s.st.Pool
	clean := func() {
		pool.Exec(ctx, `DELETE FROM user_customer_scope WHERE tenant_id IN ('itest-ds1','itest-ds2')`)
		pool.Exec(ctx, `DELETE FROM dashboards WHERE tenant_id IN ('itest-ds1','itest-ds2')`)
		pool.Exec(ctx, `UPDATE devices SET customer_id=NULL WHERE tenant_id IN ('itest-ds1','itest-ds2')`)
		pool.Exec(ctx, `DELETE FROM customers WHERE tenant_id IN ('itest-ds1','itest-ds2')`)
		pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id IN ('itest-ds1','itest-ds2')`)
		pool.Exec(ctx, `DELETE FROM users WHERE id IN ('ds-admin','ds-ua','ds-ub','ds-admin2')`)
	}
	clean()
	t.Cleanup(clean)
	for _, u := range [][2]string{{"ds-admin", "itest-ds1"}, {"ds-ua", "itest-ds1"}, {"ds-ub", "itest-ds1"}, {"ds-admin2", "itest-ds2"}} {
		pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES($1,$2,$1||'@ds-test.example','U','viewer')`, u[0], u[1])
	}
	pool.Exec(ctx, `INSERT INTO customers(id,tenant_id,name) VALUES('ds-ca','itest-ds1','A'),('ds-cb','itest-ds1','B')`)
	pool.Exec(ctx, `INSERT INTO customers(id,tenant_id,parent_id,name) VALUES('ds-ca1','itest-ds1','ds-ca','A1')`)
	for _, d := range [][2]string{{"ds1-dev-a", "ds-ca"}, {"ds1-dev-a1", "ds-ca1"}, {"ds1-dev-b", "ds-cb"}} {
		pool.Exec(ctx, `INSERT INTO devices(id,tenant_id,gateway_id,profile,name,config,customer_id) VALUES($1,'itest-ds1','itest-ds1-gw','modbus-tcp',$1,'{}',$2)`, d[0], d[1])
	}
	pool.Exec(ctx, `INSERT INTO user_customer_scope(tenant_id,user_id,customer_id) VALUES('itest-ds1','ds-ua','ds-ca'),('itest-ds1','ds-ub','ds-cb')`)
	mk := func(id, name, layout string) {
		pool.Exec(ctx, `INSERT INTO dashboards(id,tenant_id,name,layout,created_by) VALUES($1,'itest-ds1',$2,$3::jsonb,'ds-admin')`, id, name, layout)
	}
	mk("ds-d-a", "A board", `{"widgets":[{"device":"ds1-dev-a"},{"device":"ds1-dev-a1"}]}`)
	mk("ds-d-b", "B board", `{"widgets":[{"device":"ds1-dev-b"}]}`)
	mk("ds-d-open", "Unshared board", `{"widgets":[{"device":"ds1-dev-b"}]}`)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/dashboards", s.listDashboards)
	mux.HandleFunc("POST /v1/dashboards", s.saveDashboard)
	mux.HandleFunc("PUT /v1/dashboards/{id}", s.updateDashboard)
	mux.HandleFunc("DELETE /v1/dashboards/{id}", s.deleteDashboard)
	mux.HandleFunc("GET /v1/dashboards/{id}/export", s.exportDashboard)
	mux.HandleFunc("PUT /v1/dashboards/{id}/customer", s.setDashboardCustomer)
	h := s.customerScope(mux)
	adm := func(method, path, body string) int {
		return callAs(h, "itest-ds1", "ds-admin", "admin", method, path, body).Code
	}
	if adm("PUT", "/v1/dashboards/ds-d-a/customer", `{"customer_id":"ds-ca"}`) != 204 || adm("PUT", "/v1/dashboards/ds-d-b/customer", `{"customer_id":"ds-cb"}`) != 204 {
		t.Fatal("share")
	}
	// a dashboard that names another customer's device cannot be shared
	if c := adm("PUT", "/v1/dashboards/ds-d-open/customer", `{"customer_id":"ds-ca"}`); c != 409 {
		t.Fatalf("shared a layout that references another customer's device = %d", c)
	}
	if adm("PUT", "/v1/dashboards/ds-d-a/customer", `{"customer_id":"nope"}`) != 404 {
		t.Fatal("unknown customer")
	}
	// scoped users see only dashboards shared with their subtree
	la := callAs(h, "itest-ds1", "ds-ua", "viewer", "GET", "/v1/dashboards", "").Body.String()
	if !strings.Contains(la, "ds-d-a") || strings.Contains(la, "ds-d-b") || strings.Contains(la, "ds-d-open") || strings.Contains(la, "ds1-dev-b") {
		t.Fatalf("customer A user sees: %s", la)
	}
	lb := callAs(h, "itest-ds1", "ds-ub", "viewer", "GET", "/v1/dashboards", "").Body.String()
	if !strings.Contains(lb, "ds-d-b") || strings.Contains(lb, "ds-d-a") || strings.Contains(lb, "ds-d-open") {
		t.Fatalf("customer B user sees: %s", lb)
	}
	// the admin still sees all three
	if all := callAs(h, "itest-ds1", "ds-admin", "admin", "GET", "/v1/dashboards", "").Body.String(); !(strings.Contains(all, "ds-d-a") && strings.Contains(all, "ds-d-b") && strings.Contains(all, "ds-d-open")) {
		t.Fatalf("admin list: %s", all)
	}
	// scoped users cannot write, delete, export or share, even with an operator role claim
	for _, c := range [][3]string{{"POST", "/v1/dashboards", `{"name":"x","layout":{}}`}, {"PUT", "/v1/dashboards/ds-d-a", `{"name":"x","layout":{}}`},
		{"DELETE", "/v1/dashboards/ds-d-a", ""}, {"GET", "/v1/dashboards/ds-d-b/export", ""}, {"PUT", "/v1/dashboards/ds-d-b/customer", `{"customer_id":"ds-ca"}`}} {
		if code := callAs(h, "itest-ds1", "ds-ua", "operator", c[0], c[1], c[2]).Code; code != 403 {
			t.Errorf("scoped %s %s = %d, want 403", c[0], c[1], code)
		}
	}
	// editing a shared dashboard cannot add a device outside the customer
	if c := adm("PUT", "/v1/dashboards/ds-d-a", `{"name":"A board","layout":{"widgets":[{"device":"ds1-dev-b"}]}}`); c != 409 {
		t.Fatalf("layout edit leaking another customer's device = %d", c)
	}
	if c := adm("PUT", "/v1/dashboards/ds-d-a", `{"name":"A board 2","layout":{"widgets":[{"device":"ds1-dev-a1"}]}}`); c != 200 {
		t.Fatalf("clean layout edit = %d", c)
	}
	// another tenant cannot share or see them
	if callAs(h, "itest-ds2", "ds-admin2", "admin", "PUT", "/v1/dashboards/ds-d-a/customer", `{"customer_id":null}`).Code != 404 {
		t.Fatal("cross-tenant share")
	}
	if l := callAs(h, "itest-ds2", "ds-admin2", "admin", "GET", "/v1/dashboards", "").Body.String(); strings.Contains(l, "ds-d-") {
		t.Fatal("cross-tenant list")
	}
	// unsharing hides it from the customer again
	if adm("PUT", "/v1/dashboards/ds-d-a/customer", `{"customer_id":null}`) != 204 {
		t.Fatal("unshare")
	}
	if l := callAs(h, "itest-ds1", "ds-ua", "viewer", "GET", "/v1/dashboards", "").Body.String(); strings.Contains(l, "ds-d-a") {
		t.Fatalf("unshared dashboard still visible: %s", l)
	}
}
