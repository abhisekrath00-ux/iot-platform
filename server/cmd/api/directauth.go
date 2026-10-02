package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/google/uuid"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/brokeracl"
)

// Direct devices can authenticate to the broker with a client certificate
// (default, strongest) or, when a tenant admin explicitly enables it, a
// username (the serial) plus a random secret. Secrets are stored only as
// PBKDF2-SHA512 hashes and shown once. Both modes get the same
// telemetry-only ACL.

var directSerial = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{2,63}$`)

const passwordWarning = "Password auth is weaker than client certificates: the secret lives on the device, can be extracted from firmware or flash, and cannot be bound to hardware. Use it only for devices that cannot hold a private key, always over TLS, and rotate or revoke on any doubt."

func (s *server) directPasswordEnabled(r *http.Request) bool {
	var on bool
	s.st.Pool.QueryRow(r.Context(), `SELECT password_enabled FROM tenant_direct_auth WHERE tenant_id=$1`, auth.Tenant(r)).Scan(&on)
	return on
}

func (s *server) getDirectAuthPolicy(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	writeJSON(w, 200, map[string]any{"password_enabled": s.directPasswordEnabled(r), "warning": passwordWarning})
}

func (s *server) putDirectAuthPolicy(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	var in struct {
		PasswordEnabled bool `json:"password_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO tenant_direct_auth(tenant_id,password_enabled,updated_by) VALUES($1,$2,$3)
		ON CONFLICT (tenant_id) DO UPDATE SET password_enabled=$2, updated_by=$3, updated_at=now()`,
		auth.Tenant(r), in.PasswordEnabled, auth.User(r)); err != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "direct_auth.policy", auth.Tenant(r), map[string]any{"password_enabled": in.PasswordEnabled})
	// Turning it off removes every password device of the tenant from the broker files.
	s.regenerateBrokerACL(r.Context())
	writeJSON(w, 200, map[string]any{"password_enabled": in.PasswordEnabled})
}

func newBrokerSecret() (secret, hash string, err error) {
	b := make([]byte, 24)
	if _, err = rand.Read(b); err != nil {
		return
	}
	secret = base64.RawURLEncoding.EncodeToString(b)
	hash, err = brokeracl.PasswordHash(secret)
	return
}

// createPasswordDevice mints a direct device that uses username+secret.
func (s *server) createPasswordDevice(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	var in struct {
		SiteID string `json:"site_id"`
		Serial string `json:"serial"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.SiteID == "" || !directSerial.MatchString(in.Serial) {
		http.Error(w, "site_id and serial (3-64 chars: letters, digits, . _ -) required", 400)
		return
	}
	if in.Serial == "ingest" || in.Serial == "api" {
		http.Error(w, "serial is reserved", 400)
		return
	}
	if !s.directPasswordEnabled(r) {
		http.Error(w, "password auth is not enabled for this tenant: an admin must turn it on first", 403)
		return
	}
	secret, hash, err := newBrokerSecret()
	if err != nil {
		http.Error(w, "rng", 500)
		return
	}
	tenant, id := auth.Tenant(r), uuid.NewString()
	tag, err := s.st.Pool.Exec(r.Context(),
		`INSERT INTO gateways(id,tenant_id,site_id,serial,status,kind,auth_mode,broker_pw_hash,last_seen_at)
		 SELECT $1,$2,$3,$4,'active','direct','password',$5,NULL WHERE EXISTS (SELECT 1 FROM sites WHERE id=$3 AND tenant_id=$2)`,
		id, tenant, in.SiteID, in.Serial, hash)
	if err != nil {
		http.Error(w, "serial already in use", 409)
		return
	}
	if tag.RowsAffected() != 1 {
		http.Error(w, "unknown site", 404)
		return
	}
	s.audit(r, "direct_device.create", id, map[string]any{"serial": in.Serial, "auth": "password"})
	s.regenerateBrokerACL(r.Context())
	writeJSON(w, 201, map[string]any{
		"gateway_id": id, "username": in.Serial, "password": secret,
		"publish_topic": "t/" + tenant + "/g/" + id + "/telemetry",
		"warning":       passwordWarning,
		"note":          "the password is shown once and stored only as a hash",
	})
}

func (s *server) rotatePasswordDevice(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	secret, hash, err := newBrokerSecret()
	if err != nil {
		http.Error(w, "rng", 500)
		return
	}
	id := r.PathValue("id")
	tag, err := s.st.Pool.Exec(r.Context(), `UPDATE gateways SET broker_pw_hash=$3 WHERE id=$1 AND tenant_id=$2 AND auth_mode='password' AND status='active'`,
		id, auth.Tenant(r), hash)
	if err != nil || tag.RowsAffected() != 1 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "direct_device.rotate", id, nil)
	s.regenerateBrokerACL(r.Context())
	writeJSON(w, 200, map[string]any{"gateway_id": id, "password": secret, "note": "the previous password stops working once the broker reloads its password file"})
}

// revokeDirectDevice disables either kind of direct device (cert or password).
func (s *server) revokeDirectDevice(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	id := r.PathValue("id")
	tag, err := s.st.Pool.Exec(r.Context(), `UPDATE gateways SET status='revoked', broker_pw_hash=NULL WHERE id=$1 AND tenant_id=$2 AND kind='direct'`, id, auth.Tenant(r))
	if err != nil || tag.RowsAffected() != 1 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "direct_device.revoke", id, nil)
	s.regenerateBrokerACL(r.Context())
	writeJSON(w, 200, map[string]any{"revoked": id})
}

func (s *server) listDirectDevices(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT id, serial, status, auth_mode, last_seen_at FROM gateways WHERE tenant_id=$1 AND kind='direct' ORDER BY created_at DESC LIMIT 500`, auth.Tenant(r))
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, serial, status, mode string
		var seen *string
		_ = seen
		var ls any
		if err := rows.Scan(&id, &serial, &status, &mode, &ls); err == nil {
			out = append(out, map[string]any{"id": id, "serial": serial, "status": status, "auth": mode, "last_seen_at": ls})
		}
	}
	writeJSON(w, 200, out)
}
