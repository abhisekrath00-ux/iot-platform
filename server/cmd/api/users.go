package main

// Tenant user management and optional local sign-in. Local sign-in is off unless LOCAL_LOGIN=1, because
// deployments with an identity provider should keep passwords out of the platform entirely. See
// docs/saas-design.md for the model, the threats and what is not built.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

var validRoles = map[string]bool{"admin": true, "operator": true, "installer": true, "viewer": true}

const (
	maxFailedLogins = 5
	lockoutFor      = 15 * time.Minute
)

func localLoginEnabled() bool { return os.Getenv("LOCAL_LOGIN") == "1" }

// userAdminOnly: tenant admins using a session. API keys and the assistant never manage users.
func userAdminOnly(w http.ResponseWriter, r *http.Request) bool {
	if auth.ViaKey(r) {
		http.Error(w, "not available to API keys or the assistant", http.StatusForbidden)
		return false
	}
	return requireRole(w, r, "admin")
}

func (s *server) listUsers(w http.ResponseWriter, r *http.Request) {
	if !userAdminOnly(w, r) {
		return
	}
	rows, err := s.st.Pool.Query(r.Context(), `SELECT u.id, u.email, u.display_name, u.role, u.disabled_at IS NOT NULL,
		u.password_hash IS NOT NULL, u.last_login_at, s.customer_id, u.custom_role_id
		FROM users u LEFT JOIN user_customer_scope s ON s.user_id=u.id AND s.tenant_id=u.tenant_id
		WHERE u.tenant_id=$1 AND u.id NOT LIKE 'flow-%' ORDER BY u.email LIMIT 2000`, auth.Tenant(r))
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, email, name, role string
		var disabled, hasPw bool
		var last *time.Time
		var cust, crole *string
		if rows.Scan(&id, &email, &name, &role, &disabled, &hasPw, &last, &cust, &crole) == nil {
			out = append(out, map[string]any{"id": id, "email": email, "display_name": name, "role": role,
				"disabled": disabled, "has_password": hasPw, "last_login_at": last, "customer_id": cust, "custom_role_id": crole})
		}
	}
	writeJSON(w, 200, out)
}

func validDisplayName(n string) bool {
	return n != "" && len(n) <= 100 && !strings.ContainsAny(n, "<>\x00")
}

// POST /v1/users {email, display_name, role, password?}
func (s *server) createUser(w http.ResponseWriter, r *http.Request) {
	if !userAdminOnly(w, r) {
		return
	}
	var in struct {
		Email, DisplayName, Role, Password string
	}
	var raw struct {
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
		Password    string `json:"password"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&raw) != nil {
		http.Error(w, "bad json", 400)
		return
	}
	in.Email, in.DisplayName, in.Role, in.Password = strings.TrimSpace(raw.Email), strings.TrimSpace(raw.DisplayName), raw.Role, raw.Password
	if a, err := mail.ParseAddress(in.Email); err != nil || a.Address != in.Email || len(in.Email) > 254 {
		http.Error(w, "a valid email address is required", 400)
		return
	}
	if !validDisplayName(in.DisplayName) {
		http.Error(w, "display_name required (up to 100 characters, no markup)", 400)
		return
	}
	if !validRoles[in.Role] {
		http.Error(w, "role must be admin, operator, installer or viewer", 400)
		return
	}
	var hash *string
	if in.Password != "" {
		if !localLoginEnabled() {
			http.Error(w, "local sign-in is not enabled on this deployment (LOCAL_LOGIN=1)", 409)
			return
		}
		if err := auth.CheckPasswordPolicy(in.Password, in.Email); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		h, err := auth.HashPassword(in.Password)
		if err != nil {
			http.Error(w, "hash", 500)
			return
		}
		hash = &h
	}
	id := "u-" + uuid.NewString()
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO users(id,tenant_id,email,display_name,role,password_hash) VALUES($1,$2,$3,$4,$5,$6)`,
		id, auth.Tenant(r), in.Email, in.DisplayName, in.Role, hash); err != nil {
		http.Error(w, "that email address is already registered", 409)
		return
	}
	s.audit(r, "user.create", id, map[string]any{"email": in.Email, "role": in.Role, "password_set": hash != nil})
	writeJSON(w, 201, map[string]any{"id": id})
}

// PUT /v1/users/{id} {role?, display_name?, disabled?}
func (s *server) updateUser(w http.ResponseWriter, r *http.Request) {
	if !userAdminOnly(w, r) {
		return
	}
	var in struct {
		CustomRoleID *string `json:"custom_role_id"`
		Role         *string `json:"role"`
		DisplayName  *string `json:"display_name"`
		Disabled     *bool   `json:"disabled"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		http.Error(w, "bad json", 400)
		return
	}
	t, id := auth.Tenant(r), r.PathValue("id")
	tx, err := s.st.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer tx.Rollback(r.Context())
	var role string
	var disabled bool
	var curCustom *string
	// lock the tenant's admin rows so two concurrent changes cannot both remove the last admin
	if err := tx.QueryRow(r.Context(), `SELECT role, disabled_at IS NOT NULL, custom_role_id FROM users WHERE id=$1 AND tenant_id=$2 FOR UPDATE`, id, t).Scan(&role, &disabled, &curCustom); err != nil {
		http.Error(w, "user not found", 404)
		return
	}
	newRole, newDisabled := role, disabled
	newCustom := curCustom
	if in.CustomRoleID != nil {
		if *in.CustomRoleID == "" {
			newCustom = nil
		} else {
			var base string
			if err := tx.QueryRow(r.Context(), `SELECT base_role FROM custom_roles WHERE id=$1 AND tenant_id=$2`, *in.CustomRoleID, t).Scan(&base); err != nil {
				http.Error(w, "unknown custom role", 404)
				return
			}
			newRole, newCustom = base, in.CustomRoleID
		}
	}
	if in.Role != nil {
		newCustom = nil
		if !validRoles[*in.Role] {
			http.Error(w, "role must be admin, operator, installer or viewer", 400)
			return
		}
		newRole = *in.Role
	}
	if in.Disabled != nil {
		newDisabled = *in.Disabled
	}
	if in.DisplayName != nil && !validDisplayName(strings.TrimSpace(*in.DisplayName)) {
		http.Error(w, "display_name invalid", 400)
		return
	}
	if id == auth.User(r) && (newDisabled || newRole != role) {
		http.Error(w, "you cannot disable or change the role of your own account", 409)
		return
	}
	if role == "admin" && !disabled && (newRole != "admin" || newDisabled) {
		var others int
		tx.QueryRow(r.Context(), `SELECT count(*) FROM (SELECT 1 FROM users WHERE tenant_id=$1 AND role='admin' AND disabled_at IS NULL AND id<>$2 FOR UPDATE) x`, t, id).Scan(&others)
		if others == 0 {
			http.Error(w, "the tenant must keep at least one active admin", 409)
			return
		}
	}
	name := in.DisplayName
	if name != nil {
		n := strings.TrimSpace(*name)
		name = &n
	}
	if _, err := tx.Exec(r.Context(), `UPDATE users SET role=$3, custom_role_id=$6, display_name=COALESCE($4, display_name),
		disabled_at = CASE WHEN $5 THEN COALESCE(disabled_at, now()) ELSE NULL END,
		failed_logins = CASE WHEN $5 THEN failed_logins ELSE 0 END, locked_until = CASE WHEN $5 THEN locked_until ELSE NULL END
		WHERE id=$1 AND tenant_id=$2`, id, t, newRole, name, newDisabled, newCustom); err != nil || tx.Commit(r.Context()) != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "user.update", id, map[string]any{"role": newRole, "disabled": newDisabled})
	w.WriteHeader(204)
}

// PUT /v1/users/{id}/password {password}: an admin sets or resets a password.
func (s *server) resetUserPassword(w http.ResponseWriter, r *http.Request) {
	if !userAdminOnly(w, r) {
		return
	}
	if !localLoginEnabled() {
		http.Error(w, "local sign-in is not enabled on this deployment (LOCAL_LOGIN=1)", 409)
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		http.Error(w, "bad json", 400)
		return
	}
	t, id := auth.Tenant(r), r.PathValue("id")
	var email string
	if s.st.Pool.QueryRow(r.Context(), `SELECT email FROM users WHERE id=$1 AND tenant_id=$2`, id, t).Scan(&email) != nil {
		http.Error(w, "user not found", 404)
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
	s.st.Pool.Exec(r.Context(), `UPDATE users SET password_hash=$3, failed_logins=0, locked_until=NULL, tokens_valid_after=now() WHERE id=$1 AND tenant_id=$2`, id, t, h)
	s.audit(r, "user.password_reset", id, nil)
	w.WriteHeader(204)
}

// POST /v1/me/password {current, new}
func (s *server) changeOwnPassword(w http.ResponseWriter, r *http.Request) {
	if auth.ViaKey(r) || !localLoginEnabled() {
		http.Error(w, "not available", http.StatusForbidden)
		return
	}
	var in struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		http.Error(w, "bad json", 400)
		return
	}
	var email string
	var stored *string
	if s.st.Pool.QueryRow(r.Context(), `SELECT email, password_hash FROM users WHERE id=$1 AND tenant_id=$2`, auth.User(r), auth.Tenant(r)).Scan(&email, &stored) != nil || stored == nil || !auth.VerifyPassword(in.Current, *stored) {
		http.Error(w, "current password is wrong", http.StatusForbidden)
		return
	}
	if err := auth.CheckPasswordPolicy(in.New, email); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	h, _ := auth.HashPassword(in.New)
	s.st.Pool.Exec(r.Context(), `UPDATE users SET password_hash=$3, tokens_valid_after=now() WHERE id=$1 AND tenant_id=$2`, auth.User(r), auth.Tenant(r), h)
	s.audit(r, "user.password_change", auth.User(r), nil)
	w.WriteHeader(204)
}

// POST /auth/login {email, password} (public, rate limited at the mux). Same token as SSO sign-in.
func (s *server) localLogin(w http.ResponseWriter, r *http.Request) {
	if !localLoginEnabled() {
		http.Error(w, "local sign-in is not enabled", http.StatusNotFound)
		return
	}
	var in struct{ Email, Password string }
	var raw struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&raw) != nil {
		http.Error(w, "bad json", 400)
		return
	}
	in.Email, in.Password = strings.TrimSpace(raw.Email), raw.Password
	const generic = "invalid email or password"
	var id, tenant, role string
	var hash *string
	var disabled *time.Time
	var locked *time.Time
	err := s.st.Pool.QueryRow(r.Context(), `SELECT id, tenant_id, role, password_hash, disabled_at, locked_until FROM users WHERE lower(email)=lower($1)`, in.Email).
		Scan(&id, &tenant, &role, &hash, &disabled, &locked)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && hash == nil) {
		auth.DummyVerify(in.Password)
		http.Error(w, generic, 401)
		return
	}
	if err != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	if locked != nil && locked.After(time.Now()) {
		http.Error(w, "too many attempts, try again later", http.StatusTooManyRequests)
		return
	}
	ok := auth.VerifyPassword(in.Password, *hash)
	if !ok || disabled != nil {
		if !ok {
			s.st.Pool.Exec(r.Context(), `UPDATE users SET failed_logins=failed_logins+1,
				locked_until = CASE WHEN failed_logins+1 >= $2 THEN now() + make_interval(secs => $3) ELSE locked_until END WHERE id=$1`, id, maxFailedLogins, lockoutFor.Seconds())
		}
		s.st.Pool.Exec(r.Context(), `INSERT INTO audit_log(tenant_id,actor,action,target,detail) VALUES($1,$2,'auth.login_failed',$2,$3)`, tenant, id, `{"reason":"`+map[bool]string{true: "disabled", false: "bad password"}[ok]+`"}`)
		http.Error(w, generic, 401)
		return
	}
	s.st.Pool.Exec(r.Context(), `UPDATE users SET failed_logins=0, locked_until=NULL, last_login_at=now() WHERE id=$1`, id)
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{
		TenantID: tenant, Role: role,
		RegisteredClaims: jwt.RegisteredClaims{Subject: id, IssuedAt: jwt.NewNumericDate(time.Now()), ExpiresAt: jwt.NewNumericDate(time.Now().Add(12 * time.Hour))},
	})
	signed, err := tok.SignedString(s.secret)
	if err != nil {
		http.Error(w, "token", 500)
		return
	}
	s.st.Pool.Exec(r.Context(), `INSERT INTO audit_log(tenant_id,actor,action,target,detail) VALUES($1,$2,'auth.login',$2,'{}')`, tenant, id)
	writeJSON(w, 200, map[string]any{"token": signed, "user_id": id, "tenant_id": tenant, "role": role})
}

// activeUser rejects a request whose user was disabled after the token was issued. Principals with no users
// row (API-key owners are users; system principals are not) pass unchanged.
func (s *server) activeUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.ViaKey(r) { // API keys carry their own role and lifecycle; the assistant runs as the user, so a custom role still limits it
			if auth.ViaAI(r) {
				if d := s.customDenied(r); len(d) > 0 && (d[0] == "__fail_closed__" || deniedByRole(d, r.Method, r.URL.Path)) {
					http.Error(w, "your role does not allow this", http.StatusForbidden)
					return
				}
			}
			next.ServeHTTP(w, r)
			return
		}
		var off bool
		var role string
		var validAfter *time.Time
		err := s.st.Pool.QueryRow(r.Context(), `SELECT disabled_at IS NOT NULL, role, tokens_valid_after FROM users WHERE id=$1 AND tenant_id=$2`, auth.User(r), auth.Tenant(r)).Scan(&off, &role, &validAfter)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		if err == nil {
			if off {
				http.Error(w, "account disabled", http.StatusUnauthorized)
				return
			}
			if iat, ok := auth.IssuedAt(r); ok && validAfter != nil && iat.Before(validAfter.Truncate(time.Second)) {
				http.Error(w, "session revoked", http.StatusUnauthorized)
				return
			}
			// The database role is authoritative: a demotion applies to tokens already issued.
			r = auth.WithRole(r, role)
			if d := s.customDenied(r); len(d) > 0 && (d[0] == "__fail_closed__" || deniedByRole(d, r.Method, r.URL.Path)) {
				http.Error(w, "your role does not allow this", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// GET /v1/me: who the session belongs to, with the live role and customer scope. The UI uses it to hide
// pages the user cannot use; the server still enforces every rule.
func (s *server) me(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"user_id": auth.User(r), "tenant_id": auth.Tenant(r), "role": auth.Role(r), "via_key": auth.ViaKey(r)}
	var email, name string
	if s.st.Pool.QueryRow(r.Context(), `SELECT email, display_name FROM users WHERE id=$1 AND tenant_id=$2`, auth.User(r), auth.Tenant(r)).Scan(&email, &name) == nil {
		out["email"], out["display_name"] = email, name
	}
	var cid, cname string
	if s.st.Pool.QueryRow(r.Context(), `SELECT c.id, c.name FROM user_customer_scope s JOIN customers c ON c.id=s.customer_id WHERE s.tenant_id=$1 AND s.user_id=$2`, auth.Tenant(r), auth.User(r)).Scan(&cid, &cname) == nil {
		out["customer_id"], out["customer_name"] = cid, cname
	}
	if d := s.customDenied(r); len(d) > 0 && d[0] != "__fail_closed__" {
		out["denied"] = d
	}
	writeJSON(w, 200, out)
}

// POST /v1/me/sessions/revoke: sign this user out everywhere (every token issued up to now stops working,
// including the one used for this call). A session cannot be revoked by an API key or the assistant.
func (s *server) revokeOwnSessions(w http.ResponseWriter, r *http.Request) {
	if auth.ViaKey(r) {
		http.Error(w, "not available to API keys or the assistant", http.StatusForbidden)
		return
	}
	tag, err := s.st.Pool.Exec(r.Context(), `UPDATE users SET tokens_valid_after=now() WHERE id=$1 AND tenant_id=$2`, auth.User(r), auth.Tenant(r))
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "unavailable", 503)
		return
	}
	s.audit(r, "auth.sessions_revoked", auth.User(r), nil)
	w.WriteHeader(204)
}
