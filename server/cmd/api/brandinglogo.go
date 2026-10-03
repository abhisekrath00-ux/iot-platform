package main

import (
	"bytes"
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
	var b []byte
	var ct *string
	if s.st.Pool.QueryRow(r.Context(), `SELECT logo, logo_type FROM tenant_branding WHERE tenant_id=$1`, auth.Tenant(r)).Scan(&b, &ct) != nil || len(b) == 0 || ct == nil {
		http.Error(w, "no logo", 404)
		return
	}
	w.Header().Set("Content-Type", *ct)
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
