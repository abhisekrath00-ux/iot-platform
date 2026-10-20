package main

import (
	"net/http"
	"strings"
	"testing"
)

// Per-customer report branding: a report shared with a customer prints that customer's logo
// (HTML download, and the same resolution feeds PDF/XLSX and scheduled e-mail renders), then
// falls through to the tenant logo, then the built-in mark. Cross-tenant ids match nothing.
func TestIntegrationCustomerReportLogo(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-crl1")
	seed(t, s, "itest-crl2")
	ctx := t.Context()
	pool := s.st.Pool
	clean := func() {
		pool.Exec(ctx, `DELETE FROM customer_branding WHERE tenant_id IN ('itest-crl1','itest-crl2')`)
		pool.Exec(ctx, `DELETE FROM tenant_branding WHERE tenant_id IN ('itest-crl1','itest-crl2')`)
		pool.Exec(ctx, `DELETE FROM reports WHERE tenant_id IN ('itest-crl1','itest-crl2')`)
		pool.Exec(ctx, `DELETE FROM customers WHERE tenant_id IN ('itest-crl1','itest-crl2')`)
	}
	clean()
	t.Cleanup(clean)
	pool.Exec(ctx, `INSERT INTO customers(id,tenant_id,name) VALUES ('crl-c1','itest-crl1','Acme'),('crl-c2','itest-crl1','Beta'),('crl-x','itest-crl2','Other')`)
	pool.Exec(ctx, `INSERT INTO tenant_branding(tenant_id,product_name,accent,logo,logo_type) VALUES ('itest-crl1','TenantCo','#005bbb',$1,'image/png')`, []byte(pngBytes(32, 32)))
	pool.Exec(ctx, `INSERT INTO customer_branding(customer_id,tenant_id,product_name,logo,logo_type) VALUES ('crl-c1','itest-crl1','AcmeCo',$1,'image/png')`, []byte(pngBytes(64, 16)))
	pool.Exec(ctx, `INSERT INTO customer_branding(customer_id,tenant_id,product_name) VALUES ('crl-c2','itest-crl1','BetaCo')`) // row, no logo
	pool.Exec(ctx, `INSERT INTO customer_branding(customer_id,tenant_id,product_name,logo,logo_type) VALUES ('crl-x','itest-crl2','XCo',$1,'image/png')`, []byte(pngBytes(16, 16)))

	size := func(tenant, customerID string) (int, int) {
		img := s.reportLogoImageFor(ctx, tenant, customerID)
		if img == nil {
			return 0, 0
		}
		b := img.Bounds()
		return b.Dx(), b.Dy()
	}
	if w, h := size("itest-crl1", "crl-c1"); w != 64 || h != 16 {
		t.Fatalf("shared report must print the customer logo, got %dx%d", w, h)
	}
	if w, h := size("itest-crl1", "crl-c2"); w != 32 || h != 32 {
		t.Fatalf("customer row without a logo must fall through to the tenant logo, got %dx%d", w, h)
	}
	if w, h := size("itest-crl1", ""); w != 32 || h != 32 {
		t.Fatalf("unshared report must print the tenant logo, got %dx%d", w, h)
	}
	if w, h := size("itest-crl1", "crl-x"); w != 32 || h != 32 {
		t.Fatalf("a customer id from another tenant must match nothing and fall through, got %dx%d", w, h)
	}
	if img := s.reportLogoImageFor(ctx, "itest-crl2", ""); img == nil {
		t.Fatalf("tenant without a logo must fall through to the built-in mark")
	}

	// End to end through the download path: the shared report's HTML carries the customer logo
	// (wide 64x16 source rendered at 120px), an unshared report carries the tenant logo (square 32x32 -> 37px).
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/reports/{id}/download", s.downloadReport)
	mk := func(id string, customer any) {
		pool.Exec(ctx, `INSERT INTO reports(id,tenant_id,name,definition,customer_id,created_by) VALUES($1,'itest-crl1',$1,$2::jsonb,$3,'crl-admin')`,
			id, `{"metrics":[{"device_id":"crl-dev","point_id":"temp"}],"window_hours":24,"group_by":"hour"}`, customer)
	}
	mk("crl-r-shared", "crl-c1")
	mk("crl-r-plain", nil)
	w := callAs(mux, "itest-crl1", "crl-admin", "admin", "GET", "/v1/reports/crl-r-shared/download?format=html", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "width:120px") || strings.Contains(w.Body.String(), "width:37px") {
		t.Fatalf("shared report download must carry the customer logo: code %d", w.Code)
	}
	w = callAs(mux, "itest-crl1", "crl-admin", "admin", "GET", "/v1/reports/crl-r-plain/download?format=html", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "width:37px") || strings.Contains(w.Body.String(), "width:120px") {
		t.Fatalf("unshared report download must carry the tenant logo: code %d", w.Code)
	}
}
