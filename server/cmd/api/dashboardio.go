package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

const dashboardFormat = "hexmon-dashboard/1"

// GET /v1/dashboards/{id}/export: a portable copy of one dashboard. Widgets keep the device and
// point ids they were built on; those belong to the source tenant and are checked on import.
func (s *server) exportDashboard(w http.ResponseWriter, r *http.Request) {
	var name string
	var layout json.RawMessage
	if err := s.st.Pool.QueryRow(r.Context(), `SELECT name, layout FROM dashboards WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), auth.Tenant(r)).Scan(&name, &layout); err != nil {
		http.Error(w, "not found", 404)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="dashboard.json"`)
	writeJSON(w, 200, map[string]any{"format": dashboardFormat, "name": name, "layout": layout})
}

// POST /v1/dashboards/import: creates a new dashboard from an export. Widgets that point at devices
// this tenant does not have are kept but listed under "missing_devices" so the user can repoint them.
func (s *server) importDashboard(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		Format string          `json:"format"`
		Name   string          `json:"name"`
		Layout json.RawMessage `json:"layout"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10)).Decode(&in); err != nil || in.Format != dashboardFormat {
		http.Error(w, "not a "+dashboardFormat+" file", 400)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 200 {
		http.Error(w, "name required (up to 200 characters)", 400)
		return
	}
	var lay struct {
		Widgets []map[string]any `json:"widgets"`
	}
	if json.Unmarshal(in.Layout, &lay) != nil || len(lay.Widgets) > 200 {
		http.Error(w, "layout must be an object with up to 200 widgets", 400)
		return
	}
	tenant := auth.Tenant(r)
	missing := []string{}
	seen := map[string]bool{}
	for _, wd := range lay.Widgets {
		id, _ := wd["device_id"].(string)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		var one int
		if s.st.Pool.QueryRow(r.Context(), `SELECT 1 FROM devices WHERE id=$1 AND tenant_id=$2`, id, tenant).Scan(&one) != nil {
			missing = append(missing, id)
		}
	}
	id := uuid.NewString()
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO dashboards(id,tenant_id,name,layout,created_by) VALUES($1,$2,$3,$4,$5)`, id, tenant, in.Name, in.Layout, auth.User(r)); err != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "dashboard.import", id, map[string]any{"name": in.Name, "widgets": len(lay.Widgets), "missing_devices": len(missing)})
	writeJSON(w, 201, map[string]any{"id": id, "widgets": len(lay.Widgets), "missing_devices": missing})
}
