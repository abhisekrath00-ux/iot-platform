package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Per-customer branding: a scoped user sees their customer's override, falling
// through to the tenant branding per field; admins manage the override under
// /v1/customers/{id}/branding; other customers and tenants are unaffected.
func TestIntegrationCustomerBranding(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-cb1")
	seed(t, s, "itest-cb2")
	ctx := t.Context()
	pool := s.st.Pool
	clean := func() {
		pool.Exec(ctx, `DELETE FROM customer_branding WHERE tenant_id IN ('itest-cb1','itest-cb2')`)
		pool.Exec(ctx, `DELETE FROM tenant_branding WHERE tenant_id IN ('itest-cb1','itest-cb2')`)
		pool.Exec(ctx, `DELETE FROM user_customer_scope WHERE tenant_id IN ('itest-cb1','itest-cb2')`)
		pool.Exec(ctx, `DELETE FROM customers WHERE tenant_id IN ('itest-cb1','itest-cb2')`)
		pool.Exec(ctx, `DELETE FROM users WHERE id IN ('cb-admin','cb-scoped','cb-scoped2')`)
		pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id IN ('itest-cb1','itest-cb2')`)
	}
	clean()
	t.Cleanup(clean)
	pool.Exec(ctx, `INSERT INTO customers(id,tenant_id,name) VALUES ('cb-c1','itest-cb1','Acme'),('cb-c2','itest-cb1','Beta'),('cb-x','itest-cb2','Other')`)
	pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES
	  ('cb-admin','itest-cb1','cba@x.local','A','admin'),
	  ('cb-scoped','itest-cb1','cbs@x.local','S','viewer'),
	  ('cb-scoped2','itest-cb1','cbs2@x.local','T','viewer')`)
	pool.Exec(ctx, `INSERT INTO user_customer_scope(tenant_id,user_id,customer_id) VALUES ('itest-cb1','cb-scoped','cb-c1'),('itest-cb1','cb-scoped2','cb-c2')`)
	// tenant branding: name+accent+logo
	pool.Exec(ctx, `INSERT INTO tenant_branding(tenant_id,product_name,accent,logo,logo_type) VALUES ('itest-cb1','TenantCo','#005bbb',$1,'image/png')`, []byte(pngBytes(32, 32)))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/branding", s.getBranding)
	mux.HandleFunc("GET /v1/branding/logo", s.getBrandingLogo)
	mux.HandleFunc("GET /v1/customers/{id}/branding", s.getCustomerBranding)
	mux.HandleFunc("PUT /v1/customers/{id}/branding", s.putCustomerBranding)
	mux.HandleFunc("PUT /v1/customers/{id}/branding/logo", s.putCustomerBrandingLogo)
	mux.HandleFunc("GET /v1/customers/{id}/branding/logo", s.getCustomerBrandingLogo)
	mux.HandleFunc("DELETE /v1/customers/{id}/branding/logo", s.deleteCustomerBrandingLogo)
	h := s.customerScope(mux)

	readBrand := func(user string) (map[string]any, int) {
		w := callAs(h, "itest-cb1", user, "admin", "GET", "/v1/branding", "")
		var m map[string]any
		json.Unmarshal(w.Body.Bytes(), &m)
		return m, w.Code
	}

	// Admin sets the customer override: name + accent + logo. Validation matches tenant branding.
	if w := callAs(h, "itest-cb1", "cb-admin", "admin", "PUT", "/v1/customers/cb-c1/branding", `{"product_name":"Acme Portal","accent":"#b45309"}`); w.Code != 200 {
		t.Fatalf("set customer branding = %d %s", w.Code, w.Body)
	}
	if w := callAs(h, "itest-cb1", "cb-admin", "admin", "PUT", "/v1/customers/cb-c1/branding", `{"accent":"#ffcc00"}`); w.Code != 400 {
		t.Fatalf("light accent must be refused: %d", w.Code)
	}
	if w := callAs(h, "itest-cb1", "cb-admin", "admin", "PUT", "/v1/customers/cb-x/branding", `{"product_name":"x"}`); w.Code != 404 {
		t.Fatalf("cross-tenant customer = %d", w.Code)
	}
	if w := callAs(h, "itest-cb1", "cb-admin", "admin", "PUT", "/v1/customers/cb-c1/branding/logo", pngBytes(64, 64)); w.Code != 200 {
		t.Fatalf("customer logo = %d", w.Code)
	}

	// The scoped user on cb-c1 sees the override on every field.
	m, code := readBrand("cb-scoped")
	if code != 200 || m["product_name"] != "Acme Portal" || m["accent"] != "#b45309" || m["has_logo"] != true || m["customer_branding"] != true {
		t.Fatalf("scoped branding = %v (%d)", m, code)
	}
	if w := callAs(h, "itest-cb1", "cb-scoped", "viewer", "GET", "/v1/branding/logo", ""); w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("scoped logo = %d %s", w.Code, w.Header().Get("Content-Type"))
	}

	// The scoped user on cb-c2 (no override) and an unscoped admin see tenant branding.
	for _, u := range []string{"cb-scoped2", "cb-admin"} {
		m, _ := readBrand(u)
		if m["product_name"] != "TenantCo" || m["accent"] != "#005bbb" || m["customer_branding"] == true {
			t.Fatalf("%s must see tenant branding: %v", u, m)
		}
	}

	// Partial override: name only; accent and logo fall through to the tenant.
	if w := callAs(h, "itest-cb1", "cb-admin", "admin", "PUT", "/v1/customers/cb-c2/branding", `{"product_name":"Beta Hub"}`); w.Code != 200 {
		t.Fatalf("partial override = %d", w.Code)
	}
	m, _ = readBrand("cb-scoped2")
	if m["product_name"] != "Beta Hub" || m["accent"] != "#005bbb" || m["has_logo"] != true || m["customer_branding"] != true {
		t.Fatalf("partial override must fall through per field and still flag customer branding: %v", m)
	}
	if w := callAs(h, "itest-cb1", "cb-scoped2", "viewer", "GET", "/v1/branding/logo", ""); w.Code != 200 {
		t.Fatalf("tenant logo must serve through a name-only override: %d", w.Code)
	}

	// Removing the customer's logo drops back to the tenant's.
	if w := callAs(h, "itest-cb1", "cb-admin", "admin", "DELETE", "/v1/customers/cb-c1/branding/logo", ""); w.Code != 204 {
		t.Fatalf("delete customer logo = %d", w.Code)
	}
	m, _ = readBrand("cb-scoped")
	if m["has_logo"] != true || m["customer_branding"] != true {
		t.Fatalf("after logo removal the tenant logo must show, name/accent stay customer: %v", m)
	}

	// Scoped users cannot manage branding (default deny), but can read /v1/branding and the logo.
	if scopedAllows("PUT", "/v1/customers/cb-c1/branding") || scopedAllows("DELETE", "/v1/customers/cb-c1/branding/logo") {
		t.Fatal("scoped users must not manage customer branding")
	}
	if !scopedAllows("GET", "/v1/branding") || !scopedAllows("GET", "/v1/branding/logo") {
		t.Fatal("scoped users need their branding reads")
	}
	// Audit rows exist for the writes.
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id='itest-cb1' AND action LIKE 'branding.customer%'`).Scan(&n)
	if n < 3 {
		t.Fatalf("expected customer branding audit rows, got %d", n)
	}
	_ = strings.TrimSpace
	var _ = http.StatusOK
}
