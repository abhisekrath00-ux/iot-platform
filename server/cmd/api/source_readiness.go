package main

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/fleet"
	"net/http"
)

// Readiness reports only server-side prerequisites. Local edge opt-in and live
// connectivity are unknown until the gateway replies. No keys are generated.
func (s *server) sourceReadiness(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	if auth.ViaKey(r) || scopeOf(r) != "" {
		http.Error(w, "workspace interactive admin required", 403)
		return
	}
	gw, tenant := r.PathValue("id"), auth.Tenant(r)
	var serial, status string
	if err := s.st.Pool.QueryRow(r.Context(), `SELECT serial,status FROM gateways WHERE id=$1 AND tenant_id=$2`, gw, tenant).Scan(&serial, &status); err != nil {
		http.Error(w, "gateway not found", 404)
		return
	}
	devices, err := s.gatewayEdgeDevices(r, tenant, gw)
	if err != nil {
		dbErr(w, err)
		return
	}
	desired := devicesSection(renderEdgeYAML(tenant, gw, serial, devices))
	sum := sha256.Sum256([]byte(desired))
	key, err := fleet.SigningKeyFromEnv()
	signing := err == nil && key != nil
	reason := ""
	if status != "active" {
		reason = "This gateway has not finished pairing."
	} else if !signing {
		reason = "Automatic source updates are not configured on this server. An administrator must configure signing before these settings can be applied."
	}
	names := []string{}
	rows, err := s.st.Pool.Query(r.Context(), `SELECT name FROM devices WHERE tenant_id=$1 AND gateway_id=$2 ORDER BY created_at`, tenant, gw)
	if err != nil {
		dbErr(w, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			dbErr(w, err)
			return
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		dbErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"server_ready": status == "active" && signing, "reason": reason, "desired_sha256": hex.EncodeToString(sum[:]), "source_names": names, "edge_opt_in": "unknown", "connected": "unknown"})
}
