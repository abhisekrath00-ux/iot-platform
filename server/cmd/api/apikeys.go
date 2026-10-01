package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// API keys let SCADA/BI tools call the REST API without a user session.
// Format: hxk_<id>.<secret>. Only sha256(secret) is stored. Keys are tenant
// scoped, capped at operator (never admin), always expire, and cannot manage
// keys or approve control commands' four-eyes as an admin.

const maxKeyDays = 365

func hashSecret(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }

func newKeyToken() (id, secret, token string, err error) {
	b := make([]byte, 8+32)
	if _, err = rand.Read(b); err != nil {
		return
	}
	id = hex.EncodeToString(b[:8])
	secret = base64.RawURLEncoding.EncodeToString(b[8:])
	token = "hxk_" + id + "." + secret
	return
}

// resolveAPIKey implements auth.KeyResolver.
func (s *server) resolveAPIKey(ctx context.Context, token string) (string, string, string, []string, bool) {
	rest := strings.TrimPrefix(token, "hxk_")
	id, secret, ok := strings.Cut(rest, ".")
	if !ok || id == "" || secret == "" || len(id) > 32 {
		return "", "", "", nil, false
	}
	var tenant, role string
	var hash []byte
	var scopes []string
	err := s.st.Pool.QueryRow(ctx,
		`SELECT tenant_id, role, secret_hash, scopes FROM api_keys
		 WHERE id=$1 AND revoked_at IS NULL AND expires_at > now()`, id).Scan(&tenant, &role, &hash, &scopes)
	if err != nil {
		return "", "", "", nil, false
	}
	if subtle.ConstantTimeCompare(hash, hashSecret(secret)) != 1 {
		return "", "", "", nil, false
	}
	go s.st.Pool.Exec(context.Background(), `UPDATE api_keys SET last_used_at=now() WHERE id=$1`, id)
	return tenant, "apikey:" + id, role, scopes, true
}

func (s *server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	if auth.ViaKey(r) || !requireRole(w, r, "admin") {
		http.Error(w, "admin session required", 403)
		return
	}
	var in struct {
		Name   string   `json:"name"`
		Role   string   `json:"role"`
		Days   int      `json:"expires_in_days"`
		Scopes []string `json:"scopes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 80 {
		http.Error(w, "name required (max 80 chars)", 400)
		return
	}
	if in.Role == "" {
		in.Role = "viewer"
	}
	if in.Role != "viewer" && in.Role != "operator" {
		http.Error(w, "role must be viewer or operator", 400)
		return
	}
	if in.Days <= 0 || in.Days > maxKeyDays {
		http.Error(w, "expires_in_days must be 1-365", 400)
		return
	}
	id, secret, token, err := newKeyToken()
	if err != nil {
		http.Error(w, "entropy failure", 500)
		return
	}
	if len(in.Scopes) > len(grantableScopes) {
		http.Error(w, "too many scopes", 400)
		return
	}
	seen := map[string]bool{}
	for _, sc := range in.Scopes {
		if !grantableScopes[sc] || seen[sc] {
			http.Error(w, "unknown or duplicate scope "+sc, 400)
			return
		}
		seen[sc] = true
	}
	if in.Scopes == nil {
		in.Scopes = []string{}
	}
	exp := time.Now().Add(time.Duration(in.Days) * 24 * time.Hour)
	if _, err := s.st.Pool.Exec(r.Context(),
		`INSERT INTO api_keys(id,tenant_id,name,secret_hash,role,created_by,expires_at,scopes) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		id, auth.Tenant(r), in.Name, hashSecret(secret), in.Role, auth.User(r), exp, in.Scopes); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit(r, "apikey.create", id, map[string]any{"name": in.Name, "role": in.Role, "expires_at": exp, "scopes": in.Scopes})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 201, map[string]any{"id": id, "name": in.Name, "role": in.Role, "scopes": in.Scopes, "expires_at": exp, "token": token,
		"note": "Store this token now. It cannot be shown again."})
}

func (s *server) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	if auth.ViaKey(r) || !requireRole(w, r, "admin") {
		http.Error(w, "admin session required", 403)
		return
	}
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT id,name,role,created_by,created_at,expires_at,last_used_at,revoked_at,scopes FROM api_keys WHERE tenant_id=$1 ORDER BY created_at DESC`, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, role, by string
		var created, exp time.Time
		var used, rev *time.Time
		var scopes []string
		rows.Scan(&id, &name, &role, &by, &created, &exp, &used, &rev, &scopes)
		out = append(out, map[string]any{"id": id, "name": name, "role": role, "created_by": by, "created_at": created,
			"expires_at": exp, "last_used_at": used, "revoked_at": rev, "scopes": scopes})
	}
	writeJSON(w, 200, out)
}

func (s *server) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	if auth.ViaKey(r) || !requireRole(w, r, "admin") {
		http.Error(w, "admin session required", 403)
		return
	}
	id := r.PathValue("id")
	ct, err := s.st.Pool.Exec(r.Context(),
		`UPDATE api_keys SET revoked_at=now() WHERE id=$1 AND tenant_id=$2 AND revoked_at IS NULL`, id, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if ct.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "apikey.revoke", id, nil)
	writeJSON(w, 200, map[string]any{"id": id, "revoked": true})
}

// grantableScopes are the first path segments a key may be limited to.
// Administrative surfaces (api-keys, audit, broker, commands, commissioning,
// enrollment, fleet, gateways, notifications) can never be granted to a
// scoped key; an unscoped key keeps its role's existing reach.
var grantableScopes = map[string]bool{
	"alerts": true, "assets": true, "dashboards": true, "devices": true, "export": true, "flows": true, "kpis": true,
	"points": true, "profiles": true, "reports": true, "rules": true, "search": true, "telemetry": true,
}
