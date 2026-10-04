package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// Cross-customer leak tests. A customer-scoped user must see only devices in their customer subtree on every
// read path, and be refused everywhere else (default deny).
func TestIntegrationCustomerScope(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-cs1")
	seed(t, s, "itest-cs2")
	ctx := context.Background()
	pool := s.st.Pool
	clean := func() {
		pool.Exec(ctx, `DELETE FROM user_customer_scope WHERE tenant_id IN ('itest-cs1','itest-cs2')`)
		pool.Exec(ctx, `UPDATE devices SET customer_id=NULL WHERE tenant_id IN ('itest-cs1','itest-cs2')`)
		pool.Exec(ctx, `DELETE FROM alerts WHERE tenant_id IN ('itest-cs1','itest-cs2')`)
		pool.Exec(ctx, `DELETE FROM customers WHERE tenant_id IN ('itest-cs1','itest-cs2') AND parent_id IS NOT NULL`)
		pool.Exec(ctx, `DELETE FROM customers WHERE tenant_id IN ('itest-cs1','itest-cs2')`)
		pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id IN ('itest-cs1','itest-cs2')`)
	}
	clean()
	t.Cleanup(clean)
	// devices: a (customer A), a1 (sub-customer A1 of A), b (customer B), u (no customer), all in itest-cs1
	for _, d := range []string{"a", "a1", "b", "u"} {
		pool.Exec(ctx, `INSERT INTO devices(id,tenant_id,gateway_id,profile,name,config) VALUES('cs1-`+d+`','itest-cs1','itest-cs1-gw','modbus-tcp','Dev `+d+`','{}')`)
		pool.Exec(ctx, `INSERT INTO points(id,device_id,unit) VALUES('temp','cs1-`+d+`','C')`)
		pool.Exec(ctx, `INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,value,unit,schema_version)
			VALUES('cs1-e-`+d+`','itest-cs1','itest-cs1-gw','cs1-`+d+`','temp',now(),5,'C',1)`)
		pool.Exec(ctx, `INSERT INTO alerts(id,tenant_id,severity,message,device_id) VALUES('cs1-al-`+d+`','itest-cs1','warning','alert `+d+`','cs1-`+d+`')`)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/devices", s.listDevices)
	mux.HandleFunc("GET /v1/alerts", s.listAlerts)
	mux.HandleFunc("GET /v1/alerts/{id}", s.getAlert)
	mux.HandleFunc("GET /v1/devices/{id}/health", s.deviceHealth)
	mux.HandleFunc("GET /v1/telemetry/latest", s.latestTelemetry)
	mux.HandleFunc("GET /v1/telemetry/rollup", s.rollupTelemetry)
	mux.HandleFunc("GET /v1/customers", s.listCustomers)
	mux.HandleFunc("POST /v1/customers", s.createCustomer)
	mux.HandleFunc("DELETE /v1/customers/{id}", s.deleteCustomer)
	mux.HandleFunc("PUT /v1/customers/{id}/users/{user}", s.setCustomerUser)
	mux.HandleFunc("DELETE /v1/customers/{id}/users/{user}", s.setCustomerUser)
	mux.HandleFunc("PUT /v1/devices/{id}/customer", s.setDeviceCustomer)
	mux.HandleFunc("GET /v1/secrets", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("LEAK")) })
	h := s.customerScope(mux)

	mk := func(body string) string {
		w := callAs(h, "itest-cs1", "admin1", "admin", "POST", "/v1/customers", body)
		if w.Code != 201 {
			t.Fatalf("create customer %s: %d %s", body, w.Code, w.Body)
		}
		var o struct{ ID string }
		json.Unmarshal(w.Body.Bytes(), &o)
		return o.ID
	}
	A := mk(`{"name":"A"}`)
	B := mk(`{"name":"B"}`)
	A1 := mk(`{"name":"A1","parent_id":"` + A + `"}`)
	if w := callAs(h, "itest-cs1", "admin1", "admin", "POST", "/v1/customers", `{"name":"A1","parent_id":"`+A+`"}`); w.Code != 409 {
		t.Fatalf("duplicate name = %d", w.Code)
	}
	if w := callAs(h, "itest-cs2", "admin2", "admin", "POST", "/v1/customers", `{"name":"X","parent_id":"`+A+`"}`); w.Code != 404 {
		t.Fatalf("parent from another tenant = %d, want 404", w.Code)
	}
	for dev, c := range map[string]string{"a": A, "a1": A1, "b": B} {
		if w := callAs(h, "itest-cs1", "admin1", "admin", "PUT", "/v1/devices/cs1-"+dev+"/customer", `{"customer_id":"`+c+`"}`); w.Code != 204 {
			t.Fatalf("assign %s: %d %s", dev, w.Code, w.Body)
		}
	}
	// another tenant cannot assign or scope against these customers
	if w := callAs(h, "itest-cs2", "admin2", "admin", "PUT", "/v1/devices/itest-cs2-dev/customer", `{"customer_id":"`+A+`"}`); w.Code != 404 {
		t.Fatalf("cross-tenant customer assign = %d, want 404", w.Code)
	}
	if w := callAs(h, "itest-cs2", "admin2", "admin", "PUT", "/v1/customers/"+A+"/users/zed", ``); w.Code != 404 {
		t.Fatalf("cross-tenant scope = %d, want 404", w.Code)
	}
	if w := callAs(h, "itest-cs1", "admin1", "admin", "PUT", "/v1/customers/"+A+"/users/alice", ``); w.Code != 204 {
		t.Fatalf("scope alice: %d %s", w.Code, w.Body)
	}
	if w := callAs(h, "itest-cs1", "admin1", "admin", "PUT", "/v1/customers/"+B+"/users/bob", ``); w.Code != 204 {
		t.Fatal("scope bob")
	}
	if w := callAs(h, "itest-cs1", "admin1", "admin", "DELETE", "/v1/customers/"+A, ``); w.Code != 409 {
		t.Fatalf("delete non-empty customer = %d", w.Code)
	}

	ids := func(w *httptest.ResponseRecorder) string {
		var rows []map[string]any
		json.Unmarshal(w.Body.Bytes(), &rows)
		var out []string
		for _, r := range rows {
			out = append(out, r["id"].(string))
		}
		return strings.Join(out, ",")
	}
	// alice (customer A, with sub-customer A1): sees a and a1 only
	if got := ids(callAs(h, "itest-cs1", "alice", "viewer", "GET", "/v1/devices", "")); !(strings.Contains(got, "cs1-a") && strings.Contains(got, "cs1-a1")) || strings.Contains(got, "cs1-b") || strings.Contains(got, "cs1-u") || strings.Contains(got, "dev") {
		t.Fatalf("alice device list: %q", got)
	}
	// bob (customer B) sees only b, not A's subtree
	if got := ids(callAs(h, "itest-cs1", "bob", "viewer", "GET", "/v1/devices", "")); got != "cs1-b" {
		t.Fatalf("bob device list: %q", got)
	}
	// an unscoped user still sees everything in the tenant, and the shared list is not poisoned by scoped reads
	if got := ids(callAs(h, "itest-cs1", "carol", "viewer", "GET", "/v1/devices", "")); !strings.Contains(got, "cs1-b") || !strings.Contains(got, "cs1-u") || !strings.Contains(got, "cs1-a") {
		t.Fatalf("unscoped list: %q", got)
	}
	// alerts: list filtered, single read 404 outside scope
	if got := ids(callAs(h, "itest-cs1", "bob", "viewer", "GET", "/v1/alerts", "")); got != "cs1-al-b" {
		t.Fatalf("bob alert list: %q", got)
	}
	if got := ids(callAs(h, "itest-cs1", "alice", "viewer", "GET", "/v1/alerts?status=open", "")); strings.Contains(got, "al-b") || strings.Contains(got, "al-u") || !strings.Contains(got, "al-a1") {
		t.Fatalf("alice alert list: %q", got)
	}
	if w := callAs(h, "itest-cs1", "bob", "viewer", "GET", "/v1/alerts/cs1-al-a", ""); w.Code != 404 {
		t.Fatalf("bob reads A's alert: %d", w.Code)
	}
	if w := callAs(h, "itest-cs1", "alice", "viewer", "GET", "/v1/alerts/cs1-al-a1", ""); w.Code != 200 {
		t.Fatalf("alice reads sub-customer alert: %d", w.Code)
	}
	// per-device reads
	for _, p := range []string{"/v1/devices/cs1-b/health", "/v1/telemetry/latest?device_id=cs1-b", "/v1/telemetry/rollup?device_id=cs1-b&point_id=temp", "/v1/telemetry/latest?device_id=cs1-u", "/v1/telemetry/latest?device_id=itest-cs2-dev"} {
		if w := callAs(h, "itest-cs1", "alice", "viewer", "GET", p, ""); w.Code != 404 {
			t.Fatalf("alice %s = %d, want 404", p, w.Code)
		}
	}
	if w := callAs(h, "itest-cs1", "alice", "viewer", "GET", "/v1/telemetry/latest?device_id=cs1-a1", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "temp") {
		t.Fatalf("alice own sub-customer telemetry: %d %s", w.Code, w.Body)
	}
	if w := callAs(h, "itest-cs1", "alice", "viewer", "GET", "/v1/telemetry/latest", ""); w.Code != 400 {
		t.Fatalf("telemetry without device_id for scoped user = %d, want 400", w.Code)
	}
	// default deny, even for an admin-role scoped user, and no writes
	for _, c := range [][2]string{{"GET", "/v1/secrets"}, {"GET", "/v1/customers"}, {"GET", "/v1/audit"}, {"GET", "/v1/devices/cs1-a/tokens"}} {
		if w := callAs(h, "itest-cs1", "alice", "admin", c[0], c[1], ""); w.Code != 403 || strings.Contains(w.Body.String(), "LEAK") {
			t.Fatalf("scoped admin %v = %d", c, w.Code)
		}
	}
	for _, c := range [][2]string{{"POST", "/v1/customers"}, {"PUT", "/v1/devices/cs1-a/customer"}, {"PUT", "/v1/customers/" + B + "/users/alice"}, {"DELETE", "/v1/customers/" + A}} {
		if w := callAs(h, "itest-cs1", "alice", "admin", c[0], c[1], `{"name":"x"}`); w.Code != 403 {
			t.Fatalf("scoped write %v = %d", c, w.Code)
		}
	}
	// the scope belongs to the tenant: the same user id in another tenant is unscoped there
	if got := ids(callAs(h, "itest-cs2", "alice", "viewer", "GET", "/v1/devices", "")); got != "itest-cs2-dev" {
		t.Fatalf("other tenant list: %q", got)
	}
	// API keys and the assistant cannot manage customers or scopes
	r := httptest.NewRequest("POST", "/v1/customers", strings.NewReader(`{"name":"K"}`))
	w := httptest.NewRecorder()
	c2 := context.WithValue(context.WithValue(context.WithValue(r.Context(), auth.CtxTenant, "itest-cs1"), auth.CtxUser, "admin1"), auth.CtxRole, "admin")
	c2 = context.WithValue(c2, auth.CtxViaKey, true)
	h.ServeHTTP(w, r.WithContext(c2))
	if w.Code != 403 {
		t.Fatalf("api key create customer = %d", w.Code)
	}
	// removing the scope makes the user tenant-wide again
	callAs(h, "itest-cs1", "admin1", "admin", "DELETE", "/v1/customers/"+A+"/users/alice", "")
	if got := ids(callAs(h, "itest-cs1", "alice", "viewer", "GET", "/v1/devices", "")); !strings.Contains(got, "cs1-b") {
		t.Fatalf("after unscope: %q", got)
	}
}

// Every registered route is either on the scoped-user allowlist or refused. Parsing the route table from the
// source means a newly added endpoint is denied to scoped users until someone deliberately allows it.
func TestScopedUsersAreDeniedEveryUnlistedRoute(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	re := regexp.MustCompile(`(?:api\.HandleFunc|s\.cached\(api,|api\.Handle)\(?\s*"([A-Z]+) (/v1/[^"]*)"`)
	var routes [][2]string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			routes = append(routes, [2]string{m[1], m[2]})
		}
	}
	if len(routes) < 100 {
		t.Fatalf("only found %d routes; the route table moved", len(routes))
	}
	allowedGET := map[string]bool{"/v1/devices": true, "/v1/alerts": true, "/v1/alerts/{id}": true, "/v1/features": true, "/v1/map/config": true, "/v1/me": true, "/v1/devices/{id}/health": true}
	for k := range scopedTelemetry {
		allowedGET["/v1/telemetry/"+k] = true
	}
	for _, rt := range routes {
		if (rt[0] == "GET" && allowedGET[rt[1]]) || (rt[0] == "POST" && (rt[1] == "/v1/me/password" || rt[1] == "/v1/me/sessions/revoke")) {
			continue
		}
		// This check needs no database: a scoped user is simulated by calling the deny logic's inputs directly.
		if scopedAllows(rt[0], strings.NewReplacer("{id}", "x", "{fid}", "x", "{z}", "0", "{x}", "0", "{y}", "0", "{tid}", "x", "{v}", "1", "{version}", "1", "{name}", "x", "{key}", "x", "{feature}", "x", "{user}", "x", "{tenant}", "x", "{sid}", "x").Replace(rt[1])) {
			t.Errorf("scoped users would reach %s %s", rt[0], rt[1])
		}
	}
}
