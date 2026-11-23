package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/applyadmission"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/fleet"
	"github.com/jackc/pgx/v5"
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
	safetyReason := ""
	if s.applySafetyCandidate {
		safetyReason = s.candidateApplyReadiness(r.Context(), tenant, gw)
		if reason == "" && safetyReason != "" {
			reason = "Apply safety checks are not ready. No new request is allowed. Inspect the saved recovery status; do not clear or replace safety storage to retry."
		}
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
	out := map[string]any{"server_ready": status == "active" && signing && safetyReason == "", "reason": reason, "desired_sha256": hex.EncodeToString(sum[:]), "source_names": names, "edge_opt_in": "unknown", "connected": "unknown"}
	if s.applySafetyCandidate {
		out["apply_safety_reason"] = safetyReason
	}
	writeJSON(w, 200, out)
}

// Readiness is a read-only observation, not permission or a durable lease to
// publish. POST separately rechecks catalog, current history and retained store.
func (s *server) candidateApplyReadiness(ctx context.Context, tenant, gateway string) string {
	tx, err := s.st.Pool.Begin(ctx)
	if err != nil {
		return "safety_checks_unavailable"
	}
	defer tx.Rollback(context.Background())
	if err := applyadmission.Verify(ctx, tx); err != nil {
		return "safety_guard_unavailable"
	}
	if err := s.applyAttemptFence.RequireBound(); err != nil {
		return "safety_store_unavailable"
	}
	latest, err := s.applyAttemptFence.Latest(tenant, gateway)
	if err != nil {
		return "safety_store_unavailable"
	}
	var id, digest, status string
	var version int
	err = tx.QueryRow(ctx, `SELECT id,sha256,status,version FROM gateway_config_pushes WHERE tenant_id=$1 AND gateway_id=$2 ORDER BY version DESC LIMIT 1`, tenant, gateway).Scan(&id, &digest, &status, &version)
	if err == pgx.ErrNoRows {
		if latest == nil {
			return ""
		}
		return "database_history_missing"
	}
	if err != nil {
		return "safety_checks_unavailable"
	}
	return retainedApplyRecoveryReason(latest, id, digest, status, version)
}
