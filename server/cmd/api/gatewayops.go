package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// Remote gateway maintenance: fetch an edge agent's recent log lines, or restart the agent process.
// The gateway answers on t/<tenant>/g/<gw>/ops/result (stored by ingest, topic identity enforced).
// Logs: admin only, read-only, sent at once. Restart: admin requests, a DIFFERENT interactive user
// approves (no API keys, authenticator code when the workspace requires one), and the request expires
// five minutes after approval. The gateway must also have remote_restart: true in its own config file.
// Neither action reboots the machine, runs a shell or changes configuration.

type gatewayOpEnvelope struct {
	OpID       string    `json:"op_id"`
	Kind       string    `json:"kind"`
	Lines      int       `json:"lines,omitempty"`
	ApprovedBy string    `json:"approved_by,omitempty"`
	IssuedAt   time.Time `json:"issued_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func (s *server) opsPublish(topic string, payload []byte) error {
	if s.opsPublishHook != nil {
		return s.opsPublishHook(topic, payload)
	}
	return s.publishMQTT(topic, payload)
}

// POST /v1/gateways/{id}/ops  {"kind":"logs","lines":200} | {"kind":"restart"}
func (s *server) requestGatewayOp(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	if scopeOf(r) != "" {
		http.Error(w, "not available to customer-scoped users", 403)
		return
	}
	gw, tenant := r.PathValue("id"), auth.Tenant(r)
	var in struct {
		Kind  string `json:"kind"`
		Lines int    `json:"lines"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil || (in.Kind != "logs" && in.Kind != "restart") {
		http.Error(w, `kind must be "logs" or "restart"`, 400)
		return
	}
	if in.Lines == 0 {
		in.Lines = 200
	}
	if in.Lines < 1 || in.Lines > 500 {
		http.Error(w, "lines must be 1 to 500", 400)
		return
	}
	var status string
	if err := s.st.Pool.QueryRow(r.Context(), `SELECT status FROM gateways WHERE id=$1 AND tenant_id=$2`, gw, tenant).Scan(&status); err != nil {
		http.Error(w, "gateway not found", 404)
		return
	}
	if status != "active" {
		http.Error(w, "gateway has not claimed yet", 409)
		return
	}
	var open int
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM gateway_ops WHERE tenant_id=$1 AND gateway_id=$2 AND status IN ('pending_approval','requested') AND created_at > now() - interval '10 minutes'`, tenant, gw).Scan(&open)
	if open > 0 {
		http.Error(w, "an operation is already open on this gateway; wait for it to finish", 409)
		return
	}
	id := uuid.NewString()
	st := "requested"
	if in.Kind == "restart" {
		st = "pending_approval"
	}
	pj, _ := json.Marshal(map[string]any{"lines": in.Lines})
	if _, err := s.st.Pool.Exec(r.Context(),
		`INSERT INTO gateway_ops(id,tenant_id,gateway_id,kind,status,params,requested_by) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		id, tenant, gw, in.Kind, st, pj, auth.User(r)); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit(r, "gateway.op.request", gw, map[string]any{"kind": in.Kind, "op_id": id})
	if in.Kind == "restart" {
		writeJSON(w, 202, map[string]any{"op_id": id, "status": "pending_approval"})
		return
	}
	if err := s.sendGatewayOp(tenant, gw, id, "logs", in.Lines, ""); err != nil {
		s.st.Pool.Exec(r.Context(), `UPDATE gateway_ops SET status='failed', result=$1, updated_at=now() WHERE id=$2`, `{"ok":false,"detail":"broker unreachable"}`, id)
		http.Error(w, "broker unreachable: "+err.Error(), 502)
		return
	}
	writeJSON(w, 202, map[string]any{"op_id": id, "status": "requested"})
}

func (s *server) sendGatewayOp(tenant, gw, id, kind string, lines int, approver string) error {
	now := time.Now()
	exp := now.Add(5 * time.Minute)
	b, _ := json.Marshal(gatewayOpEnvelope{OpID: id, Kind: kind, Lines: lines, ApprovedBy: approver, IssuedAt: now, ExpiresAt: exp})
	return s.opsPublish(fmt.Sprintf("t/%s/g/%s/ops", tenant, gw), b)
}

// POST /v1/gateway-ops/{id}/approve  (restart only)
func (s *server) approveGatewayOp(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	if auth.ViaKey(r) {
		http.Error(w, "approval requires an interactive user session", 403)
		return
	}
	id, tenant := r.PathValue("id"), auth.Tenant(r)
	if s.featureEnabled(r, featureTOTP) {
		var in codeIn
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 512)).Decode(&in)
		if _, enrolled, _, _ := s.totpSecret(r); !enrolled {
			http.Error(w, "this workspace requires an authenticator code to approve: enroll one under Settings first", 403)
			return
		}
		if !s.verifyTOTP(r, in.Code) {
			s.audit(r, "gateway.op.approve_refused", id, map[string]any{"reason": "bad or reused authenticator code"})
			http.Error(w, "authenticator code missing, wrong or already used", 403)
			return
		}
	}
	var gw string
	err := s.st.Pool.QueryRow(r.Context(),
		`UPDATE gateway_ops SET status='requested', approved_by=$1, updated_at=now()
		 WHERE id=$2 AND tenant_id=$3 AND kind='restart' AND status='pending_approval' AND requested_by<>$1
		   AND created_at > now() - interval '10 minutes'
		 RETURNING gateway_id`, auth.User(r), id, tenant).Scan(&gw)
	if err != nil {
		http.Error(w, "not found, already decided, expired, or self-approval", 409)
		return
	}
	s.audit(r, "gateway.op.approve", gw, map[string]any{"op_id": id})
	if err := s.sendGatewayOp(tenant, gw, id, "restart", 0, auth.User(r)); err != nil {
		s.st.Pool.Exec(r.Context(), `UPDATE gateway_ops SET status='failed', result=$1, updated_at=now() WHERE id=$2`, `{"ok":false,"detail":"broker unreachable"}`, id)
		http.Error(w, "broker unreachable: "+err.Error(), 502)
		return
	}
	writeJSON(w, 200, map[string]any{"op_id": id, "status": "requested"})
}

// GET /v1/gateways/{id}/ops  newest first. Results of old open requests read as expired.
func (s *server) listGatewayOps(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	gw, tenant := r.PathValue("id"), auth.Tenant(r)
	s.st.Pool.Exec(r.Context(), `UPDATE gateway_ops SET status='expired', updated_at=now() WHERE tenant_id=$1 AND gateway_id=$2 AND status IN ('pending_approval','requested') AND created_at < now() - interval '10 minutes'`, tenant, gw)
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT id, kind, status, requested_by, COALESCE(approved_by,''), COALESCE(result,'null'::jsonb), created_at, updated_at
		 FROM gateway_ops WHERE tenant_id=$1 AND gateway_id=$2 ORDER BY created_at DESC LIMIT 20`, tenant, gw)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, kind, st, by, ap string
		var res json.RawMessage
		var c, u time.Time
		if rows.Scan(&id, &kind, &st, &by, &ap, &res, &c, &u) != nil {
			continue
		}
		out = append(out, map[string]any{"id": id, "kind": kind, "status": st, "requested_by": by, "approved_by": ap, "result": res, "created_at": c, "updated_at": u})
	}
	writeJSON(w, 200, out)
}
