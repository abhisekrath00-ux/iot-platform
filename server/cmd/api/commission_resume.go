package main

import (
	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"net/http"
	"time"
)

// listCommissionSessions exposes recovery identifiers, never the one-time code.
// A reload can resume a saved session without creating another gateway.
func (s *server) listCommissionSessions(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	rows, err := s.st.Pool.Query(r.Context(), `SELECT c.id,c.gateway_id,c.serial,c.device_id,g.status,c.updated_at FROM commissioning_sessions c JOIN gateways g ON g.id=c.gateway_id AND g.tenant_id=c.tenant_id WHERE c.tenant_id=$1 AND g.status<>'revoked' ORDER BY c.updated_at DESC,c.id DESC LIMIT 50`, auth.Tenant(r))
	if err != nil {
		dbErr(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, gw, serial, status string
		var device *string
		var updated time.Time
		if err := rows.Scan(&id, &gw, &serial, &device, &status, &updated); err != nil {
			dbErr(w, err)
			return
		}
		out = append(out, map[string]any{"session_id": id, "gateway_id": gw, "serial": serial, "gateway_status": status, "device_id": device, "updated_at": updated})
	}
	if err := rows.Err(); err != nil {
		dbErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, out)
}
