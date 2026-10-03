package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// Invitations are one-time links an admin hands over by any channel they trust. The platform sends no email
// (air-gapped deployments have no mail relay). The token is shown once and only its SHA-256 is stored.
const inviteTTL = 7 * 24 * time.Hour

// POST /v1/users/invites {email, role}: admin session only, needs local sign-in (the invitee sets a password).
func (s *server) createInvite(w http.ResponseWriter, r *http.Request) {
	if !userAdminOnly(w, r) {
		return
	}
	if !localLoginEnabled() {
		http.Error(w, "invitations need local sign-in (LOCAL_LOGIN=1); with single sign-on add the user directly", 409)
		return
	}
	var raw struct {
		Email      string  `json:"email"`
		Role       string  `json:"role"`
		CustomerID *string `json:"customer_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&raw) != nil {
		http.Error(w, "bad json", 400)
		return
	}
	email := strings.TrimSpace(raw.Email)
	if a, err := mail.ParseAddress(email); err != nil || a.Address != email || len(email) > 254 {
		http.Error(w, "a valid email address is required", 400)
		return
	}
	if !validRoles[raw.Role] {
		http.Error(w, "role must be admin, operator, installer or viewer", 400)
		return
	}
	if raw.CustomerID != nil {
		var ok bool
		s.st.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM customers WHERE id=$1 AND tenant_id=$2)`, *raw.CustomerID, auth.Tenant(r)).Scan(&ok)
		if !ok {
			http.Error(w, "unknown customer", 404)
			return
		}
	}
	var taken bool
	s.st.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM users WHERE lower(email)=lower($1))`, email).Scan(&taken)
	if taken {
		http.Error(w, "that email address is already registered", 409)
		return
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		http.Error(w, "entropy", 500)
		return
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	id := "inv-" + uuid.NewString()
	exp := time.Now().Add(inviteTTL)
	// one open invitation per address: a new one replaces the old
	s.st.Pool.Exec(r.Context(), `DELETE FROM user_invites WHERE tenant_id=$1 AND lower(email)=lower($2) AND used_at IS NULL`, auth.Tenant(r), email)
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO user_invites(id,tenant_id,email,role,token_hash,created_by,expires_at,customer_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		id, auth.Tenant(r), email, raw.Role, hashSecret(token), auth.User(r), exp, raw.CustomerID); err != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	s.audit(r, "user.invite", id, map[string]any{"email": email, "role": raw.Role})
	writeJSON(w, 201, map[string]any{"id": id, "token": token, "path": "/accept-invite#" + token, "expires_at": exp})
}

func (s *server) listInvites(w http.ResponseWriter, r *http.Request) {
	if !userAdminOnly(w, r) {
		return
	}
	rows, err := s.st.Pool.Query(r.Context(), `SELECT id,email,role,created_at,expires_at FROM user_invites
		WHERE tenant_id=$1 AND used_at IS NULL AND expires_at>now() ORDER BY created_at DESC LIMIT 200`, auth.Tenant(r))
	if err != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, email, role string
		var c, e time.Time
		if rows.Scan(&id, &email, &role, &c, &e) == nil {
			out = append(out, map[string]any{"id": id, "email": email, "role": role, "created_at": c, "expires_at": e})
		}
	}
	writeJSON(w, 200, out)
}

func (s *server) revokeInvite(w http.ResponseWriter, r *http.Request) {
	if !userAdminOnly(w, r) {
		return
	}
	tag, err := s.st.Pool.Exec(r.Context(), `DELETE FROM user_invites WHERE id=$1 AND tenant_id=$2 AND used_at IS NULL`, r.PathValue("id"), auth.Tenant(r))
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "user.invite_revoke", r.PathValue("id"), nil)
	w.WriteHeader(204)
}

// POST /auth/accept-invite {token, display_name, password}: unauthenticated, rate limited. One use.
func (s *server) acceptInvite(w http.ResponseWriter, r *http.Request) {
	if !localLoginEnabled() {
		http.Error(w, "local sign-in is not enabled", http.StatusNotFound)
		return
	}
	var in struct {
		Token       string `json:"token"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || len(in.Token) < 20 || len(in.Token) > 100 {
		http.Error(w, "invalid or expired invitation", 400)
		return
	}
	name := strings.TrimSpace(in.DisplayName)
	if !validDisplayName(name) {
		http.Error(w, "name required (up to 100 characters, no markup)", 400)
		return
	}
	tx, err := s.st.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	defer tx.Rollback(r.Context())
	var invID, tenant, email, role string
	var cust *string
	if err := tx.QueryRow(r.Context(), `SELECT id,tenant_id,email,role,customer_id FROM user_invites WHERE token_hash=$1 AND used_at IS NULL AND expires_at>now() FOR UPDATE`, hashSecret(in.Token)).
		Scan(&invID, &tenant, &email, &role, &cust); err != nil {
		auth.DummyVerify(in.Password)
		http.Error(w, "invalid or expired invitation", 400)
		return
	}
	if err := auth.CheckPasswordPolicy(in.Password, email); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	h, err := auth.HashPassword(in.Password)
	if err != nil {
		http.Error(w, "hash", 500)
		return
	}
	uid := "u-" + uuid.NewString()
	if _, err := tx.Exec(r.Context(), `INSERT INTO users(id,tenant_id,email,display_name,role,password_hash,last_login_at) VALUES($1,$2,$3,$4,$5,$6,now())`, uid, tenant, email, name, role, h); err != nil {
		http.Error(w, "invalid or expired invitation", 400)
		return
	}
	if cust != nil {
		if _, err := tx.Exec(r.Context(), `INSERT INTO user_customer_scope(tenant_id,user_id,customer_id) VALUES($1,$2,$3)`, tenant, uid, *cust); err != nil {
			http.Error(w, "invalid or expired invitation", 400)
			return
		}
	}
	tx.Exec(r.Context(), `UPDATE user_invites SET used_at=now() WHERE id=$1`, invID)
	tx.Exec(r.Context(), `INSERT INTO audit_log(tenant_id,actor,action,target,detail) VALUES($1,$2,'user.invite_accept',$2,$3)`, tenant, uid, `{"invite":"`+invID+`"}`)
	if tx.Commit(r.Context()) != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{TenantID: tenant, Role: role,
		RegisteredClaims: jwt.RegisteredClaims{Subject: uid, IssuedAt: jwt.NewNumericDate(time.Now()), ExpiresAt: jwt.NewNumericDate(time.Now().Add(12 * time.Hour))}})
	signed, err := tok.SignedString(s.secret)
	if err != nil {
		http.Error(w, "token", 500)
		return
	}
	writeJSON(w, 200, map[string]any{"token": signed, "user_id": uid, "tenant_id": tenant, "role": role})
}
