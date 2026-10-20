package main

import (
	"bytes"
	"context"
	_ "embed"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

const logoMax = 100 << 10

// PUT /v1/branding/logo (admin): raw PNG or JPEG body up to 100 KB, at most 1024 px on a side. The bytes are
// decoded, so a file that only claims to be an image is refused.
func (s *server) putBrandingLogo(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
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
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO tenant_branding(tenant_id,product_name,accent,logo,logo_type,updated_by) VALUES($1,'','',$2,$3,$4)
		ON CONFLICT (tenant_id) DO UPDATE SET logo=$2, logo_type=$3, updated_by=$4, updated_at=now()`, auth.Tenant(r), b, "image/"+format, auth.User(r)); err != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "branding.logo", auth.Tenant(r), map[string]any{"bytes": len(b), "type": format})
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *server) getBrandingLogo(w http.ResponseWriter, r *http.Request) {
	b, cts, rerr := s.resolveBrandingLogo(r)
	if rerr != nil {
		http.Error(w, "no logo", 404)
		return
	}
	w.Header().Set("Content-Type", cts)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Write(b)
}

func (s *server) deleteBrandingLogo(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	s.st.Pool.Exec(r.Context(), `UPDATE tenant_branding SET logo=NULL, logo_type=NULL, updated_by=$2, updated_at=now() WHERE tenant_id=$1`, auth.Tenant(r), auth.User(r))
	s.audit(r, "branding.logo.remove", auth.Tenant(r), nil)
	w.WriteHeader(204)
}

// tenantLogoImage returns the tenant's decoded logo, or nil when there is none or it cannot be decoded.
func (s *server) tenantLogoImage(ctx context.Context, tenant string) image.Image {
	var b []byte
	if s.st.Pool.QueryRow(ctx, `SELECT logo FROM tenant_branding WHERE tenant_id=$1`, tenant).Scan(&b) != nil || len(b) == 0 {
		return nil
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil
	}
	return img
}

//go:embed reportlogo-default.png
var defaultReportLogoPNG []byte

// reportLogoImageFor is reportLogoImage with per-customer resolution: a report shared with a customer
// prints that customer's logo when it has one, then falls through to the tenant logo, then the built-in
// mark - the same order /v1/branding resolves. No walk up the customer tree; a customer id from another
// tenant matches nothing and falls through.
func (s *server) reportLogoImageFor(ctx context.Context, tenant, customerID string) image.Image {
	if customerID != "" {
		var b []byte
		if err := s.st.Pool.QueryRow(ctx, `SELECT logo FROM customer_branding WHERE customer_id=$1 AND tenant_id=$2`, customerID, tenant).Scan(&b); err == nil && len(b) > 0 {
			if img, _, err := image.Decode(bytes.NewReader(b)); err == nil {
				return img
			}
		}
	}
	return s.reportLogoImage(ctx, tenant)
}

// reportLogoImage is the logo printed on reports: the tenant's own logo when it uploaded one, otherwise the
// built-in HexThings mark. Tenant white-label branding always wins.
func (s *server) reportLogoImage(ctx context.Context, tenant string) image.Image {
	if img := s.tenantLogoImage(ctx, tenant); img != nil {
		return img
	}
	img, _, err := image.Decode(bytes.NewReader(defaultReportLogoPNG))
	if err != nil {
		return nil
	}
	return img
}

// deref flattens a nullable string column to "" for resolution calls.
func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
