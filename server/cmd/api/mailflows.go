package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"net/mail"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/notify"
)

// Optional email flows: invitation mail and self-service password reset. Both are OFF unless the operator
// configures outgoing mail (SMTP_HOST, ALERT_FROM_EMAIL) and a public address for links (APP_PUBLIC_URL, or
// API_PUBLIC_URL when the app is served from the same origin). Nothing here needs the internet unless the
// chosen SMTP relay does. Links always use the configured address, never the request's Host header.

const resetTTL = time.Hour

func (s *server) mailer() replier {
	if s.notifier != nil {
		return s.notifier
	}
	return notify.FromEnv()
}

func (s *server) mailConfigured() bool {
	if s.notifier != nil {
		return os.Getenv("TEST_MAIL") == "1" // tests inject a fake and opt in explicitly
	}
	return notify.FromEnv().Configured() && appPublicURL() != ""
}

func appPublicURL() string {
	u := os.Getenv("APP_PUBLIC_URL")
	if u == "" {
		u = os.Getenv("API_PUBLIC_URL")
	}
	return strings.TrimRight(u, "/")
}

// emailInvite sends the invitation link. It reports whether mail went out; a failure never loses the invite,
// because the admin still has the link.
func (s *server) emailInvite(ctx context.Context, to, path, tenantName string) bool {
	body := "You have been invited to " + tenantName + ".\n\nOpen this link to choose a password and join (it works once and expires in 7 days):\n" +
		appPublicURL() + path + "\n\nIf you did not expect this, ignore this message.\n"
	if err := s.mailer().Email(ctx, []string{to}, "You have been invited to "+tenantName, body); err != nil {
		log.Printf("invite mail failed: %v", err)
		return false
	}
	return true
}

// POST /auth/forgot {email} (public, rate limited). The answer never says whether the address is known.
func (s *server) forgotPassword(w http.ResponseWriter, r *http.Request) {
	if !localLoginEnabled() || !s.mailConfigured() {
		http.Error(w, "password reset by email is not enabled", http.StatusNotFound)
		return
	}
	var in struct {
		Email string `json:"email"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&in) != nil {
		http.Error(w, "bad json", 400)
		return
	}
	email := strings.TrimSpace(in.Email)
	if a, err := mail.ParseAddress(email); err != nil || a.Address != email || len(email) > 254 {
		http.Error(w, "a valid email address is required", 400)
		return
	}
	var id, tenant string
	var hasPw bool
	var off *time.Time
	if s.st.Pool.QueryRow(r.Context(), `SELECT id, tenant_id, password_hash IS NOT NULL, disabled_at FROM users WHERE lower(email)=lower($1)`, email).Scan(&id, &tenant, &hasPw, &off) == nil && hasPw && off == nil {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err == nil {
			token := base64.RawURLEncoding.EncodeToString(b)
			s.st.Pool.Exec(r.Context(), `DELETE FROM password_resets WHERE user_id=$1 AND used_at IS NULL`, id)
			if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO password_resets(id,user_id,tenant_id,token_hash,expires_at) VALUES($1,$2,$3,$4,$5)`,
				"pr-"+uuid.NewString(), id, tenant, hashSecret(token), time.Now().Add(resetTTL)); err == nil {
				s.st.Pool.Exec(r.Context(), `INSERT INTO audit_log(tenant_id,actor,action,target,detail) VALUES($1,$2,'auth.password_reset_requested',$2,'{}')`, tenant, id)
				body := "Someone asked to reset the password for this address.\n\nOpen this link to choose a new password (it works once and expires in 1 hour):\n" +
					appPublicURL() + "/reset-password#" + token + "\n\nIf this was not you, ignore this message; your password has not changed.\n"
				// sent in the background so the response time does not reveal whether the address has an account
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					if err := s.mailer().Email(ctx, []string{email}, "Reset your password", body); err != nil {
						log.Printf("reset mail failed: %v", err)
					}
				}()
			}
		}
	}
	writeJSON(w, 202, map[string]any{"status": "if that address has an account, a reset link is on its way"})
}

// POST /auth/reset {token, password} (public, rate limited): set a new password with a one-time link. The
// user's authenticator, if any, still applies at the next sign-in; all earlier sessions end.
func (s *server) resetPassword(w http.ResponseWriter, r *http.Request) {
	if !localLoginEnabled() || !s.mailConfigured() {
		http.Error(w, "password reset by email is not enabled", http.StatusNotFound)
		return
	}
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || in.Token == "" {
		http.Error(w, "bad json", 400)
		return
	}
	const bad = "this reset link is invalid or has expired"
	tx, err := s.st.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	defer tx.Rollback(r.Context())
	var uid, tenant, email string
	if tx.QueryRow(r.Context(), `SELECT p.user_id, p.tenant_id, u.email FROM password_resets p JOIN users u ON u.id=p.user_id
		WHERE p.token_hash=$1 AND p.used_at IS NULL AND p.expires_at>now() AND u.disabled_at IS NULL FOR UPDATE OF p`, hashSecret(in.Token)).Scan(&uid, &tenant, &email) != nil {
		http.Error(w, bad, 400)
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
	tx.Exec(r.Context(), `UPDATE password_resets SET used_at=now() WHERE user_id=$1 AND used_at IS NULL`, uid)
	tx.Exec(r.Context(), `UPDATE users SET password_hash=$2, failed_logins=0, locked_until=NULL, tokens_valid_after=now() WHERE id=$1`, uid, h)
	tx.Exec(r.Context(), `INSERT INTO audit_log(tenant_id,actor,action,target,detail) VALUES($1,$2,'auth.password_reset',$2,'{}')`, tenant, uid)
	if tx.Commit(r.Context()) != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	w.WriteHeader(204)
}
