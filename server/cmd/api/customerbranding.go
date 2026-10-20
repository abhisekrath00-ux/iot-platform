package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"io"
	"net/http"
	"strings"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// Per-customer branding (migration 0078): an admin can give one customer or
// sub-customer its own product name, accent colour and logo. Users scoped to
// that customer see them; everyone else sees the tenant branding. Resolution
// for a scoped caller is the scoped customer's row, then the tenant row, then
// defaults - no walk up the customer tree (a sub-customer without its own row
// shows tenant branding, documented). Tenant admins manage it under
// /v1/customers/{id}/branding; the same validation as tenant branding applies
// (40-char name, #rrggbb accent with 4.5:1 contrast against white, PNG/JPEG
// logo 16-1024 px and 100 KB, decoded never trusted).

// scopedCustomer returns the customer the caller is scoped to, if any.
func (s *server) scopedCustomer(r *http.Request) string {
	var cid string
	if s.st.Pool.QueryRow(r.Context(), `SELECT customer_id FROM user_customer_scope WHERE tenant_id=$1 AND user_id=$2`,
		auth.Tenant(r), auth.User(r)).Scan(&cid) != nil {
		return ""
	}
	return cid
}

// resolveBranding is the read path shared by /v1/branding: customer override for
// scoped callers, tenant row otherwise. Missing pieces fall through: a customer
// that sets only a name keeps the tenant accent and logo.
func (s *server) resolveBranding(r *http.Request) (name, accent string, hasLogo, fromCustomer bool) {
	tenant := auth.Tenant(r)
	if cid := s.scopedCustomer(r); cid != "" {
		var cName, cAccent string
		var cLogo bool
		if s.st.Pool.QueryRow(r.Context(), `SELECT product_name, accent, logo IS NOT NULL FROM customer_branding WHERE customer_id=$1 AND tenant_id=$2`, cid, tenant).
			Scan(&cName, &cAccent, &cLogo) == nil {
			name, accent, hasLogo = cName, cAccent, cLogo
			// true when the caller sees anything customer-specific at all
			fromCustomer = cName != "" || cAccent != "" || cLogo
		}
	}
	var tName, tAccent string
	var tLogo bool
	s.st.Pool.QueryRow(r.Context(), `SELECT product_name, accent, logo IS NOT NULL FROM tenant_branding WHERE tenant_id=$1`, tenant).
		Scan(&tName, &tAccent, &tLogo)
	if name == "" {
		name = tName
	}
	if accent == "" {
		accent = tAccent
	}
	if !hasLogo {
		hasLogo = tLogo
	}
	return name, accent, hasLogo, fromCustomer
}

// resolveBrandingLogo returns the logo bytes for the same resolution order:
// the customer's own logo when set, else the tenant's.
func (s *server) resolveBrandingLogo(r *http.Request) ([]byte, string, error) {
	tenant := auth.Tenant(r)
	if cid := s.scopedCustomer(r); cid != "" {
		var b []byte
		var ct *string
		if err := s.st.Pool.QueryRow(r.Context(), `SELECT logo, logo_type FROM customer_branding WHERE customer_id=$1 AND tenant_id=$2`, cid, tenant).Scan(&b, &ct); err == nil && len(b) > 0 && ct != nil {
			return b, *ct, nil
		}
	}
	var b []byte
	var ct *string
	err := s.st.Pool.QueryRow(r.Context(), `SELECT logo, logo_type FROM tenant_branding WHERE tenant_id=$1`, tenant).Scan(&b, &ct)
	if err != nil || len(b) == 0 || ct == nil {
		return nil, "", errors.New("no logo")
	}
	return b, *ct, nil
}

// customerInTenant verifies the customer belongs to this tenant (404 otherwise).
func (s *server) customerInTenant(r *http.Request, id string) bool {
	var ok bool
	return s.st.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM customers WHERE id=$1 AND tenant_id=$2)`, id, auth.Tenant(r)).Scan(&ok) == nil && ok
}

func validBrandingInput(w http.ResponseWriter, r *http.Request) (name, accent string, ok bool) {
	var in struct {
		ProductName string `json:"product_name"`
		Accent      string `json:"accent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return "", "", false
	}
	name = strings.TrimSpace(in.ProductName)
	if len(name) > 40 || strings.ContainsAny(name, "<>&\"'\x00") {
		http.Error(w, "product_name: up to 40 characters, no markup", 400)
		return "", "", false
	}
	accent = in.Accent
	if accent != "" {
		if !hexColor.MatchString(accent) {
			http.Error(w, "accent must be #rrggbb", 400)
			return "", "", false
		}
		if contrastWithWhite(accent) < 4.5 {
			http.Error(w, "accent is too light: white text on it needs contrast of at least 4.5:1", 400)
			return "", "", false
		}
		accent = strings.ToLower(accent)
	}
	return name, accent, true
}

// GET /v1/customers/{id}/branding (admin): the customer's override row, or
// empty values when none is set. Also answers has_logo and the inherited
// tenant values so the editor can say what applies without an override.
func (s *server) getCustomerBranding(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	id := r.PathValue("id")
	if !s.customerInTenant(r, id) {
		http.Error(w, "not found", 404)
		return
	}
	var name, accent string
	var hasLogo bool
	s.st.Pool.QueryRow(r.Context(), `SELECT product_name, accent, logo IS NOT NULL FROM customer_branding WHERE customer_id=$1 AND tenant_id=$2`, id, auth.Tenant(r)).Scan(&name, &accent, &hasLogo)
	writeJSON(w, 200, map[string]any{"customer_id": id, "product_name": name, "accent": accent, "has_logo": hasLogo})
}

// PUT /v1/customers/{id}/branding (admin): set or clear (empty values) the
// customer's name/accent override. Logo is managed on the /logo sub-route.
func (s *server) putCustomerBranding(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	id := r.PathValue("id")
	if !s.customerInTenant(r, id) {
		http.Error(w, "not found", 404)
		return
	}
	name, accent, ok := validBrandingInput(w, r)
	if !ok {
		return
	}
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO customer_branding(customer_id,tenant_id,product_name,accent,updated_by) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT (customer_id) DO UPDATE SET product_name=$3, accent=$4, updated_by=$5, updated_at=now()`,
		id, auth.Tenant(r), name, accent, auth.User(r)); err != nil {
		dbErr(w, err)
		return
	}
	s.audit(r, "branding.customer_update", id, map[string]any{"product_name": name, "accent": accent})
	writeJSON(w, 200, map[string]any{"ok": true})
}

// PUT /v1/customers/{id}/branding/logo (admin): same rules as the tenant logo.
func (s *server) putCustomerBrandingLogo(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	id := r.PathValue("id")
	if !s.customerInTenant(r, id) {
		http.Error(w, "not found", 404)
		return
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, logoMax+1))
	if err != nil || len(b) > logoMax {
		http.Error(w, "logo too large (100 KB max)", 413)
		return
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || (format != "png" && format != "jpeg") {
		http.Error(w, "logo must be a PNG or JPEG image", 400)
		return
	}
	if cfg.Width < 16 || cfg.Height < 16 || cfg.Width > 1024 || cfg.Height > 1024 {
		http.Error(w, "logo must be between 16 and 1024 pixels on each side", 400)
		return
	}
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO customer_branding(customer_id,tenant_id,logo,logo_type,updated_by) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT (customer_id) DO UPDATE SET logo=$3, logo_type=$4, updated_by=$5, updated_at=now()`,
		id, auth.Tenant(r), b, "image/"+format, auth.User(r)); err != nil {
		dbErr(w, err)
		return
	}
	s.audit(r, "branding.customer_logo", id, map[string]any{"bytes": len(b), "type": format})
	writeJSON(w, 200, map[string]any{"ok": true})
}

// GET /v1/customers/{id}/branding/logo (admin): preview the customer's logo.
func (s *server) getCustomerBrandingLogo(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	var b []byte
	var ct *string
	if s.st.Pool.QueryRow(r.Context(), `SELECT logo, logo_type FROM customer_branding WHERE customer_id=$1 AND tenant_id=$2`, r.PathValue("id"), auth.Tenant(r)).Scan(&b, &ct) != nil || len(b) == 0 || ct == nil {
		http.Error(w, "no logo", 404)
		return
	}
	w.Header().Set("Content-Type", *ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(b)
}

// DELETE /v1/customers/{id}/branding/logo (admin): drop the customer's logo;
// scoped users then see the tenant logo again.
func (s *server) deleteCustomerBrandingLogo(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	id := r.PathValue("id")
	if !s.customerInTenant(r, id) {
		http.Error(w, "not found", 404)
		return
	}
	if _, err := s.st.Pool.Exec(r.Context(), `UPDATE customer_branding SET logo=NULL, logo_type=NULL, updated_by=$3, updated_at=now() WHERE customer_id=$1 AND tenant_id=$2`, id, auth.Tenant(r), auth.User(r)); err != nil {
		dbErr(w, err)
		return
	}
	s.audit(r, "branding.customer_logo_removed", id, nil)
	w.WriteHeader(204)
}
