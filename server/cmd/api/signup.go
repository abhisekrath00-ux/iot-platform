package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/mail"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// Self sign-up is OFF by default. SELF_SIGNUP=1 (with LOCAL_LOGIN=1) lets a visitor create a workspace and
// become its first admin. It needs no internet and sends no email, so the address is NOT verified: treat a
// sign-up as untrusted until the operator says otherwise. Safeguards: a hard cap on total tenants
// (SELF_SIGNUP_MAX_TENANTS, default 50), a strict rate limit at the mux, the usual password policy, and the
// default quota row ('*' in tenantctl quota) which the operator should set before turning this on.

func selfSignupEnabled() bool { return os.Getenv("SELF_SIGNUP") == "1" && localLoginEnabled() }

func selfSignupCap() int {
	if n, err := strconv.Atoi(os.Getenv("SELF_SIGNUP_MAX_TENANTS")); err == nil && n > 0 {
		return n
	}
	return 50
}

// GET /auth/config (public): lets the sign-in page know which options to show.
func (s *server) authConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"self_signup": selfSignupEnabled(), "local_login": localLoginEnabled(), "password_reset": localLoginEnabled() && s.mailConfigured(), "email_invites": s.mailConfigured()})
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func tenantSlug(name string) string {
	sl := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(sl) > 28 {
		sl = strings.Trim(sl[:28], "-")
	}
	if sl == "" {
		sl = "workspace"
	}
	return sl
}

// POST /auth/signup {workspace, email, password, display_name} (public, rate limited at the mux).
func (s *server) selfSignup(w http.ResponseWriter, r *http.Request) {
	if !selfSignupEnabled() {
		http.Error(w, "sign-up is not enabled", http.StatusNotFound)
		return
	}
	var in struct {
		Workspace   string `json:"workspace"`
		Email       string `json:"email"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		http.Error(w, "bad json", 400)
		return
	}
	in.Workspace, in.Email, in.DisplayName = strings.TrimSpace(in.Workspace), strings.TrimSpace(in.Email), strings.TrimSpace(in.DisplayName)
	if in.Workspace == "" || len(in.Workspace) > 128 {
		http.Error(w, "a workspace name (up to 128 characters) is required", 400)
		return
	}
	if a, err := mail.ParseAddress(in.Email); err != nil || a.Address != in.Email || len(in.Email) > 254 {
		http.Error(w, "a valid email address is required", 400)
		return
	}
	if in.DisplayName == "" {
		in.DisplayName = "Administrator"
	}
	if len(in.DisplayName) > 128 {
		http.Error(w, "name too long", 400)
		return
	}
	if err := auth.CheckPasswordPolicy(in.Password, in.Email); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		http.Error(w, "hash", 500)
		return
	}
	suffix := make([]byte, 3)
	rand.Read(suffix)
	tenant := tenantSlug(in.Workspace) + "-" + hex.EncodeToString(suffix)
	uid := "u-" + tenant + "-admin"
	tx, err := s.st.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	defer tx.Rollback(r.Context())
	// the cap is checked under a lock so concurrent sign-ups cannot overshoot it
	tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(hashtext('self-signup'))`)
	var n int
	tx.QueryRow(r.Context(), `SELECT count(*) FROM tenants`).Scan(&n)
	if n >= selfSignupCap() {
		http.Error(w, "sign-ups are closed on this deployment", http.StatusForbidden)
		return
	}
	var taken bool
	tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM users WHERE lower(email)=lower($1))`, in.Email).Scan(&taken)
	if taken {
		http.Error(w, "that email is already registered", http.StatusConflict)
		return
	}
	if _, err := tx.Exec(r.Context(), `INSERT INTO tenants(id,name) VALUES($1,$2)`, tenant, in.Workspace); err != nil {
		http.Error(w, "could not create the workspace", 500)
		return
	}
	if _, err := tx.Exec(r.Context(), `INSERT INTO users(id,tenant_id,email,display_name,role,password_hash,last_login_at) VALUES($1,$2,$3,$4,'admin',$5,now())`, uid, tenant, in.Email, in.DisplayName, hash); err != nil {
		http.Error(w, "that email is already registered", http.StatusConflict)
		return
	}
	tx.Exec(r.Context(), `INSERT INTO audit_log(tenant_id,actor,action,target,detail) VALUES($1,$2,'tenant.self_signup',$1,'{"email_verified":false}')`, tenant, uid)
	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, "could not create the workspace", 500)
		return
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{
		TenantID: tenant, Role: "admin",
		RegisteredClaims: jwt.RegisteredClaims{Subject: uid, IssuedAt: jwt.NewNumericDate(time.Now()), ExpiresAt: jwt.NewNumericDate(time.Now().Add(12 * time.Hour))},
	})
	signed, err := tok.SignedString(s.secret)
	if err != nil {
		http.Error(w, "token", 500)
		return
	}
	writeJSON(w, 201, map[string]any{"token": signed, "user_id": uid, "tenant_id": tenant, "role": "admin"})
}
