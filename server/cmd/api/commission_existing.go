package main

import (
	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/google/uuid"
	"net/http"
)

// Starts source setup on an already paired gateway. It does not create a
// gateway, rotate identity, mint a claim code or start source polling.
func (s *server) createExistingSourceSession(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	gw, tenant := r.PathValue("id"), auth.Tenant(r)
	var serial, site, status string
	if err := s.st.Pool.QueryRow(r.Context(), `SELECT serial,site_id,status FROM gateways WHERE id=$1 AND tenant_id=$2`, gw, tenant).Scan(&serial, &site, &status); err != nil {
		http.Error(w, "gateway not found", 404)
		return
	}
	if status != "active" {
		http.Error(w, "finish pairing this gateway before adding a source", 409)
		return
	}
	s.withIdempotency(w, r, "gateway.source-session", []byte(gw), func(w http.ResponseWriter) {
		id := uuid.NewString()
		_, err := s.st.Pool.Exec(r.Context(), `INSERT INTO commissioning_sessions(id,tenant_id,site_id,gateway_id,serial,created_by,state) VALUES($1,$2,$3,$4,$5,$6,'claimed')`, id, tenant, site, gw, serial, auth.User(r))
		if err != nil {
			dbErr(w, err)
			return
		}
		s.audit(r, "commission.source.start", id, map[string]any{"gateway_id": gw})
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 201, map[string]any{"session_id": id, "gateway_id": gw, "site_id": site, "serial": serial, "gateway_status": status, "state": "claimed", "device_id": nil})
	})
}
