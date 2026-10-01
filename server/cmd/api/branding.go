package main

import (
	"encoding/json"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func channelLum(v uint64) float64 {
	c := float64(v) / 255
	if c <= 0.03928 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// contrastWithWhite returns the WCAG contrast ratio of white text on the colour.
func contrastWithWhite(hex string) float64 {
	r, _ := strconv.ParseUint(hex[1:3], 16, 8)
	g, _ := strconv.ParseUint(hex[3:5], 16, 8)
	b, _ := strconv.ParseUint(hex[5:7], 16, 8)
	l := 0.2126*channelLum(r) + 0.7152*channelLum(g) + 0.0722*channelLum(b)
	return 1.05 / (l + 0.05)
}

func (s *server) getBranding(w http.ResponseWriter, r *http.Request) {
	var name, accent string
	s.st.Pool.QueryRow(r.Context(), `SELECT product_name, accent FROM tenant_branding WHERE tenant_id=$1`, auth.Tenant(r)).Scan(&name, &accent)
	writeJSON(w, 200, map[string]any{"product_name": name, "accent": accent})
}

// putBranding lets an admin set the product name and accent colour shown in the
// UI. The accent must give white button text at least 4.5:1 contrast, so a
// brand colour cannot make the interface unreadable. Empty values reset to defaults.
func (s *server) putBranding(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	var in struct {
		ProductName string `json:"product_name"`
		Accent      string `json:"accent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	in.ProductName = strings.TrimSpace(in.ProductName)
	if len(in.ProductName) > 40 || strings.ContainsAny(in.ProductName, "<>&\"'\x00") {
		http.Error(w, "product_name: up to 40 characters, no markup", 400)
		return
	}
	if in.Accent != "" {
		if !hexColor.MatchString(in.Accent) {
			http.Error(w, "accent must be #rrggbb", 400)
			return
		}
		if contrastWithWhite(in.Accent) < 4.5 {
			http.Error(w, "accent is too light: white text on it needs contrast of at least 4.5:1", 400)
			return
		}
	}
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO tenant_branding(tenant_id,product_name,accent,updated_by) VALUES($1,$2,$3,$4)
		ON CONFLICT (tenant_id) DO UPDATE SET product_name=$2, accent=$3, updated_by=$4, updated_at=now()`,
		auth.Tenant(r), in.ProductName, strings.ToLower(in.Accent), auth.User(r)); err != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "branding.update", auth.Tenant(r), map[string]any{"product_name": in.ProductName, "accent": in.Accent})
	writeJSON(w, 200, map[string]any{"ok": true})
}
