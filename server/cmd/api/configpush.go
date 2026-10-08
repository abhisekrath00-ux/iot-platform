package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/fleet"
)

// Device template push. The server is the source of truth: a device's connection settings plus its
// profile's points (the device template) are rendered into a device list, signed with the fleet key and
// sent to the gateway on t/<tenant>/g/<gw>/config. The gateway applies it only if it opted in
// (managed_devices: true), checks signature, tenant, serial, expiry, version order and the same rules as a
// hand-written config, keeps the previous list, restarts, and puts the previous list back if it does not
// come up healthy. Only the device list travels: never broker, TLS, identity or security settings.
// Admin and interactive only: not API keys and not the assistant.

type configPushEnvelope struct {
	PushID      string    `json:"push_id"`
	TenantID    string    `json:"tenant_id"`
	Serial      string    `json:"gateway_serial"`
	Version     int       `json:"version"`
	DevicesYAML string    `json:"devices_yaml"`
	SHA256      string    `json:"sha256"`
	ApprovedBy  string    `json:"approved_by,omitempty"`
	IssuedAt    time.Time `json:"issued_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Signature   string    `json:"signature"`
}

// devicesSection returns only the "devices:" part of the rendered agent YAML.
func devicesSection(full string) string {
	i := strings.Index(full, "\ndevices:\n")
	if i < 0 {
		return "devices:\n  []\n"
	}
	return full[i+1:]
}

// POST /v1/gateways/{id}/config-push
func (s *server) pushGatewayConfig(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	if auth.ViaKey(r) || scopeOf(r) != "" {
		http.Error(w, "not available to API keys, the assistant or customer-scoped users", 403)
		return
	}
	key, kerr := fleet.SigningKeyFromEnv()
	if kerr != nil || key == nil {
		http.Error(w, "pushing device templates needs FLEET_SIGNING_KEY on the server and fleet_public_key on the gateway", 409)
		return
	}
	tenant, gw := auth.Tenant(r), r.PathValue("id")
	var serial, status string
	if err := s.st.Pool.QueryRow(r.Context(), `SELECT serial, status FROM gateways WHERE id=$1 AND tenant_id=$2`, gw, tenant).Scan(&serial, &status); err != nil {
		http.Error(w, "gateway not found", 404)
		return
	}
	if status != "active" {
		http.Error(w, "gateway has not claimed yet", 409)
		return
	}
	s.st.Pool.Exec(r.Context(), `UPDATE gateway_config_pushes SET status='expired', detail='no answer from the gateway', updated_at=now() WHERE tenant_id=$1 AND gateway_id=$2 AND status='sent' AND created_at < now() - interval '15 minutes'`, tenant, gw)
	var open int
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM gateway_config_pushes WHERE tenant_id=$1 AND gateway_id=$2 AND status IN ('sent','applied')`, tenant, gw).Scan(&open)
	if open > 0 {
		http.Error(w, "a push is still in progress on this gateway; wait for it to be confirmed or rolled back", 409)
		return
	}
	devs, err := s.gatewayEdgeDevices(r, tenant, gw)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	yml := devicesSection(renderEdgeYAML(tenant, gw, serial, devs))
	sum := sha256.Sum256([]byte(yml))
	sha := hex.EncodeToString(sum[:])
	id := uuid.NewString()
	var ver int
	s.st.Pool.QueryRow(r.Context(), `SELECT COALESCE(max(version),0)+1 FROM gateway_config_pushes WHERE gateway_id=$1`, gw).Scan(&ver)
	now := time.Now()
	exp := now.Add(15 * time.Minute)
	env := configPushEnvelope{PushID: id, TenantID: tenant, Serial: serial, Version: ver, DevicesYAML: yml, SHA256: sha, ApprovedBy: auth.User(r), IssuedAt: now, ExpiresAt: exp,
		Signature: fleet.SignConfigPush(key, tenant, serial, ver, id, sha, exp)}
	if _, err := s.st.Pool.Exec(r.Context(),
		`INSERT INTO gateway_config_pushes(id,tenant_id,gateway_id,version,devices_yaml,sha256,status,requested_by) VALUES($1,$2,$3,$4,$5,$6,'sent',$7)`,
		id, tenant, gw, ver, yml, sha, auth.User(r)); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit(r, "gateway.config.push", gw, map[string]any{"push_id": id, "version": ver, "devices": len(devs)})
	b, _ := json.Marshal(env)
	if err := s.opsPublish(fmt.Sprintf("t/%s/g/%s/config", tenant, gw), b); err != nil {
		s.st.Pool.Exec(r.Context(), `UPDATE gateway_config_pushes SET status='failed', detail='broker unreachable', updated_at=now() WHERE id=$1`, id)
		http.Error(w, "broker unreachable: "+err.Error(), 502)
		return
	}
	writeJSON(w, 202, map[string]any{"push_id": id, "version": ver, "devices": len(devs), "status": "sent"})
}

// GET /v1/gateways/{id}/config-push  newest first.
func (s *server) listConfigPushes(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	tenant, gw := auth.Tenant(r), r.PathValue("id")
	s.st.Pool.Exec(r.Context(), `UPDATE gateway_config_pushes SET status='expired', detail='no answer from the gateway', updated_at=now() WHERE tenant_id=$1 AND gateway_id=$2 AND status='sent' AND created_at < now() - interval '15 minutes'`, tenant, gw)
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT id, version, status, detail, requested_by, sha256, created_at, updated_at FROM gateway_config_pushes WHERE tenant_id=$1 AND gateway_id=$2 ORDER BY version DESC LIMIT 20`, tenant, gw)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, st, detail, by, sha string
		var ver int
		var c, u time.Time
		if rows.Scan(&id, &ver, &st, &detail, &by, &sha, &c, &u) == nil {
			out = append(out, map[string]any{"id": id, "version": ver, "status": st, "detail": detail, "requested_by": by, "sha256": sha, "created_at": c, "updated_at": u})
		}
	}
	writeJSON(w, 200, out)
}
