package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestIntegrationScopedReports(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-rs1")
	ctx := context.Background()
	pool := s.st.Pool
	clean := func() {
		pool.Exec(ctx, `DELETE FROM user_customer_scope WHERE tenant_id='itest-rs1'`)
		pool.Exec(ctx, `DELETE FROM report_versions WHERE report_id LIKE 'rs-r-%'`)
		pool.Exec(ctx, `DELETE FROM reports WHERE tenant_id='itest-rs1'`)
		pool.Exec(ctx, `UPDATE devices SET customer_id=NULL WHERE tenant_id='itest-rs1'`)
		pool.Exec(ctx, `DELETE FROM customers WHERE tenant_id='itest-rs1'`)
		pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id='itest-rs1'`)
		pool.Exec(ctx, `DELETE FROM users WHERE id IN ('rs-admin','rs-ua','rs-ub')`)
	}
	clean()
	t.Cleanup(clean)
	for _, u := range []string{"rs-admin", "rs-ua", "rs-ub"} {
		pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES($1,'itest-rs1',$1||'@rs-test.example','U','viewer')`, u)
	}
	pool.Exec(ctx, `INSERT INTO customers(id,tenant_id,name) VALUES('rs-ca','itest-rs1','A'),('rs-cb','itest-rs1','B')`)
	for _, d := range [][2]string{{"rs-dev-a", "rs-ca"}, {"rs-dev-b", "rs-cb"}} {
		pool.Exec(ctx, `INSERT INTO devices(id,tenant_id,gateway_id,profile,name,config,customer_id) VALUES($1,'itest-rs1','itest-rs1-gw','modbus-tcp',$1,'{}',$2)`, d[0], d[1])
	}
	pool.Exec(ctx, `INSERT INTO user_customer_scope(tenant_id,user_id,customer_id) VALUES('itest-rs1','rs-ua','rs-ca'),('itest-rs1','rs-ub','rs-cb')`)
	mk := func(id, dev string) {
		pool.Exec(ctx, `INSERT INTO reports(id,tenant_id,name,definition,schedule_cron,created_by) VALUES($1,'itest-rs1',$1,$2::jsonb,'0 6 * * *','rs-admin')`,
			id, `{"metrics":[{"device_id":"`+dev+`","point_id":"temp"}],"window_hours":24,"group_by":"hour"}`)
	}
	mk("rs-r-a", "rs-dev-a")
	mk("rs-r-b", "rs-dev-b")
	mk("rs-r-open", "rs-dev-b")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/reports", s.listReports)
	mux.HandleFunc("GET /v1/reports/{id}/download", s.downloadReport)
	mux.HandleFunc("PUT /v1/reports/{id}", s.updateReport)
	mux.HandleFunc("PUT /v1/reports/{id}/customer", s.setReportCustomer)
	mux.HandleFunc("POST /v1/reports/{id}/run", s.runReport)
	h := s.customerScope(mux)
	adm := func(method, path, body string) int {
		return callAs(h, "itest-rs1", "rs-admin", "admin", method, path, body).Code
	}
	if adm("PUT", "/v1/reports/rs-r-a/customer", `{"customer_id":"rs-ca"}`) != 204 || adm("PUT", "/v1/reports/rs-r-b/customer", `{"customer_id":"rs-cb"}`) != 204 {
		t.Fatal("share")
	}
	if c := adm("PUT", "/v1/reports/rs-r-open/customer", `{"customer_id":"rs-ca"}`); c != 409 {
		t.Fatalf("shared a report using another customer's device = %d", c)
	}
	la := callAs(h, "itest-rs1", "rs-ua", "viewer", "GET", "/v1/reports", "").Body.String()
	if !strings.Contains(la, "rs-r-a") || strings.Contains(la, "rs-r-b") || strings.Contains(la, "rs-r-open") || strings.Contains(la, "0 6") {
		t.Fatalf("customer A user sees: %s", la)
	}
	if all := callAs(h, "itest-rs1", "rs-admin", "admin", "GET", "/v1/reports", "").Body.String(); !strings.Contains(all, "rs-r-open") {
		t.Fatal("admin list")
	}
	get := func(user, id, q string) int {
		return callAs(h, "itest-rs1", user, "viewer", "GET", "/v1/reports/"+id+"/download?format=csv"+q, "").Code
	}
	if c := get("rs-ua", "rs-r-a", ""); c != 200 {
		t.Fatalf("own report download = %d %s", c, callAs(h, "itest-rs1", "rs-ua", "viewer", "GET", "/v1/reports/rs-r-a/download?format=csv", "").Body.String())
	}
	if c := get("rs-ua", "rs-r-b", ""); c != 404 {
		t.Fatalf("other customer's report download = %d", c)
	}
	if c := get("rs-ua", "rs-r-open", ""); c != 404 {
		t.Fatalf("unshared report download = %d", c)
	}
	if c := get("rs-ua", "rs-r-a", "&device=rs-dev-b"); c != 403 {
		t.Fatalf("device override by scoped user = %d", c)
	}
	// no run, edit or share by a scoped user
	for _, c := range [][3]string{{"POST", "/v1/reports/rs-r-a/run", ""}, {"PUT", "/v1/reports/rs-r-a", `{}`}, {"PUT", "/v1/reports/rs-r-a/customer", `{"customer_id":null}`}} {
		if code := callAs(h, "itest-rs1", "rs-ua", "operator", c[0], c[1], c[2]).Code; code != 403 {
			t.Errorf("scoped %s %s = %d, want 403", c[0], c[1], code)
		}
	}
	// editing a shared report cannot add another customer's device
	if c := adm("PUT", "/v1/reports/rs-r-a", `{"name":"A","definition":{"metrics":[{"device_id":"rs-dev-b","point_id":"temp"}],"window_hours":24,"group_by":"hour"}}`); c != 409 {
		t.Fatalf("edit leaking another customer's device = %d", c)
	}
	if adm("PUT", "/v1/reports/rs-r-a/customer", `{"customer_id":null}`) != 204 {
		t.Fatal("unshare")
	}
	if c := get("rs-ua", "rs-r-a", ""); c != 404 {
		t.Fatalf("unshared report still downloadable = %d", c)
	}
}
