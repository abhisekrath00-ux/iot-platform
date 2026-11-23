package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/applyadmission"
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

// This marker is committed BEFORE the one publish attempt. A crash or ambiguous
// publish return cannot turn it into a retryable failure or time-expired row.
// Existing gateway-scoped sent->applied|rejected receipts can still resolve it.
const configPushPublicationUnknown = "Publication response unconfirmed. Keep the edge stopped and ask an administrator to inspect this exact request. Do not send another Apply."

// POST /v1/gateways/{id}/config-push
func (s *server) pushGatewayConfig(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	if auth.ViaKey(r) || scopeOf(r) != "" {
		http.Error(w, "not available to API keys, the assistant or customer-scoped users", 403)
		return
	}
	var in struct {
		ExpectedSHA256 string `json:"expected_sha256"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in); err != nil && err != io.EOF {
		http.Error(w, "invalid request", 400)
		return
	}
	key, kerr := fleet.SigningKeyFromEnv()
	if kerr != nil || key == nil {
		http.Error(w, "pushing device templates needs FLEET_SIGNING_KEY on the server and fleet_public_key on the gateway", 409)
		return
	}
	tenant, gw := auth.Tenant(r), r.PathValue("id")
	tx, err := s.st.Pool.Begin(r.Context())
	if err != nil {
		dbErr(w, err)
		return
	}
	defer tx.Rollback(context.Background())
	if s.applySafetyCandidate {
		if err := applyadmission.Verify(r.Context(), tx); err != nil {
			http.Error(w, "Apply safety checks unavailable. Keep the edge stopped; no new Apply was sent.", http.StatusServiceUnavailable)
			return
		}
	}
	// Serialize admission on the durable gateway row across API processes.
	// The transaction ends BEFORE broker I/O, never holding a lease during publish.
	var serial, status string
	if err := tx.QueryRow(r.Context(), `SELECT serial, status FROM gateways WHERE id=$1 AND tenant_id=$2 FOR UPDATE`, gw, tenant).Scan(&serial, &status); err != nil {
		http.Error(w, "gateway not found", 404)
		return
	}
	if status != "active" {
		http.Error(w, "gateway has not claimed yet", 409)
		return
	}
	if _, err := tx.Exec(r.Context(), `UPDATE gateway_config_pushes SET status='expired', detail='no answer from the gateway', updated_at=now() WHERE tenant_id=$1 AND gateway_id=$2 AND status='sent' AND detail<>$3 AND created_at < now() - interval '15 minutes'`, tenant, gw, configPushPublicationUnknown); err != nil {
		dbErr(w, err)
		return
	}
	var open int
	if err := tx.QueryRow(r.Context(), `SELECT count(*) FROM gateway_config_pushes WHERE tenant_id=$1 AND gateway_id=$2 AND status IN ('sent','applied')`, tenant, gw).Scan(&open); err != nil {
		dbErr(w, err)
		return
	}
	if open > 0 {
		http.Error(w, "a push is still in progress on this gateway; wait for it to be confirmed or rolled back", 409)
		return
	}
	devs, err := s.gatewayEdgeDevices(r, tenant, gw)
	if err != nil {
		dbErr(w, err)
		return
	}
	yml := devicesSection(renderEdgeYAML(tenant, gw, serial, devs))
	sum := sha256.Sum256([]byte(yml))
	sha := hex.EncodeToString(sum[:])
	if in.ExpectedSHA256 != "" && in.ExpectedSHA256 != sha {
		http.Error(w, "saved sources changed since review; reload and review the current list before applying", 409)
		return
	}
	id := uuid.NewString()
	var ver int
	if err := tx.QueryRow(r.Context(), `SELECT COALESCE(max(version),0)+1 FROM gateway_config_pushes WHERE gateway_id=$1`, gw).Scan(&ver); err != nil {
		dbErr(w, err)
		return
	}
	now := time.Now()
	exp := now.Add(15 * time.Minute)
	env := configPushEnvelope{PushID: id, TenantID: tenant, Serial: serial, Version: ver, DevicesYAML: yml, SHA256: sha, ApprovedBy: auth.User(r), IssuedAt: now, ExpiresAt: exp,
		Signature: fleet.SignConfigPush(key, tenant, serial, ver, id, sha, exp)}
	if _, err := tx.Exec(r.Context(),
		`INSERT INTO gateway_config_pushes(id,tenant_id,gateway_id,version,devices_yaml,sha256,status,requested_by,detail) VALUES($1,$2,$3,$4,$5,$6,'sent',$7,$8)`,
		id, tenant, gw, ver, yml, sha, auth.User(r), configPushPublicationUnknown); err != nil {
		dbErr(w, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		dbErr(w, err)
		return
	}
	s.audit(r, "gateway.config.push", gw, map[string]any{"push_id": id, "version": ver, "devices": len(devs)})
	b, _ := json.Marshal(env)
	publish := func() error { return s.opsPublish(fmt.Sprintf("t/%s/g/%s/config", tenant, gw), b) }
	if s.applySafetyCandidate {
		publish = func() error {
			// Existing hook remains test-only. Production candidate uses its
			// own one-connection publisher, never the reconnecting shared client.
			if s.opsPublishHook != nil {
				return s.opsPublishHook(fmt.Sprintf("t/%s/g/%s/config", tenant, gw), b)
			}
			return publishApplyMQTTOnceCandidate(r.Context(), fmt.Sprintf("t/%s/g/%s/config", tenant, gw), b)
		}
	}
	if err := publish(); err != nil {
		// Acceptance may have happened before the acknowledgement was lost.
		// Leave the precommitted marker, never retry or label it a failed send.
		http.Error(w, configPushPublicationUnknown, 502)
		return
	}
	// Do not overwrite a gateway receipt that raced with the broker return.
	if _, err := s.st.Pool.Exec(r.Context(), `UPDATE gateway_config_pushes SET detail='', updated_at=now() WHERE id=$1 AND tenant_id=$2 AND gateway_id=$3 AND status='sent' AND detail=$4`, id, tenant, gw, configPushPublicationUnknown); err != nil {
		http.Error(w, configPushPublicationUnknown, 502)
		return
	}
	writeJSON(w, 202, map[string]any{"push_id": id, "version": ver, "devices": len(devs), "status": "sent", "sha256": sha})
}

// GET /v1/gateways/{id}/config-push  newest first.
func (s *server) listConfigPushes(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	tenant, gw := auth.Tenant(r), r.PathValue("id")
	if _, err := s.st.Pool.Exec(r.Context(), `UPDATE gateway_config_pushes SET status='expired', detail='no answer from the gateway', updated_at=now() WHERE tenant_id=$1 AND gateway_id=$2 AND status='sent' AND detail<>$3 AND created_at < now() - interval '15 minutes'`, tenant, gw, configPushPublicationUnknown); err != nil {
		dbErr(w, err)
		return
	}
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT id, version, status, detail, requested_by, sha256, created_at, updated_at FROM gateway_config_pushes WHERE tenant_id=$1 AND gateway_id=$2 ORDER BY version DESC LIMIT 20`, tenant, gw)
	if err != nil {
		dbErr(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, st, detail, by, sha string
		var ver int
		var c, u time.Time
		if rows.Scan(&id, &ver, &st, &detail, &by, &sha, &c, &u) == nil {
			out = append(out, map[string]any{"id": id, "version": ver, "status": st, "detail": detail, "requested_by": by, "sha256": sha, "created_at": c, "updated_at": u, "publication_unconfirmed": st == "sent" && detail == configPushPublicationUnknown})
		}
	}
	writeJSON(w, 200, out)
}
