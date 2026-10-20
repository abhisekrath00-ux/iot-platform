package main

import (
	"net"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// Tracked sessions: every login (local, SSO, invite accept) stores a
// user_sessions row whose id rides in the token as jti, so a user can list
// and revoke individual sessions. Tokens minted before this feature carry no
// jti and stay valid until they expire (12 hours at most); "sign out
// everywhere" (tokens_valid_after) still covers them.

// startSession records a new login and returns its session id.
func (s *server) startSession(r *http.Request, tenant, user string) string {
	id := uuid.NewString()
	ua := r.UserAgent()
	if len(ua) > 200 {
		ua = ua[:200]
	}
	ip := r.RemoteAddr
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host // the ephemeral port changes per connection; the host identifies the device
	}
	if len(ip) > 64 {
		ip = ip[:64]
	}
	if _, err := s.st.Pool.Exec(r.Context(),
		`INSERT INTO user_sessions(id,tenant_id,user_id,ip,user_agent) VALUES($1,$2,$3,$4,$5)`, id, tenant, user, ip, ua); err != nil {
		return "" // a session row is best-effort at login; the token below still works and expires in 12h
	}
	return id
}

// sessionToken mints the 12-hour session token; sid may be "" (untracked).
func (s *server) sessionToken(tenant, role, uid, sid string) (string, error) {
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{
		TenantID: tenant, Role: role,
		RegisteredClaims: jwt.RegisteredClaims{ID: sid, Subject: uid, IssuedAt: jwt.NewNumericDate(time.Now()), ExpiresAt: jwt.NewNumericDate(time.Now().Add(12 * time.Hour))},
	})
	return tok.SignedString(s.secret)
}

// revokeSessionRows marks every live session row revoked (paired with a
// tokens_valid_after bump, which is what actually kills jti-less tokens).
func (s *server) revokeSessionRows(r *http.Request, tenant, user string) {
	s.st.Pool.Exec(r.Context(), `UPDATE user_sessions SET revoked_at=now() WHERE tenant_id=$1 AND user_id=$2 AND revoked_at IS NULL`, tenant, user)
}

// GET /v1/me/sessions: this user's live sessions, newest first; the one
// carrying this request is flagged. API keys and the assistant have no sessions.
func (s *server) listOwnSessions(w http.ResponseWriter, r *http.Request) {
	if auth.ViaKey(r) {
		http.Error(w, "not available to API keys or the assistant", http.StatusForbidden)
		return
	}
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT id, created_at, last_seen_at, COALESCE(ip,''), COALESCE(user_agent,'')
		 FROM user_sessions WHERE tenant_id=$1 AND user_id=$2 AND revoked_at IS NULL
		 ORDER BY created_at DESC LIMIT 50`, auth.Tenant(r), auth.User(r))
	if err != nil {
		dbErr(w, err)
		return
	}
	defer rows.Close()
	cur := auth.SessionID(r)
	out := []map[string]any{}
	for rows.Next() {
		var id, ip, ua string
		var created, seen time.Time
		if rows.Scan(&id, &created, &seen, &ip, &ua) == nil {
			out = append(out, map[string]any{"id": id, "created_at": created, "last_seen_at": seen, "ip": ip, "user_agent": ua, "current": id == cur})
		}
	}
	writeJSON(w, 200, out)
}

// DELETE /v1/me/sessions/{id}: revoke one of this user's sessions. Revoking
// the current one signs this request's token out at once. Another user's id
// answers 404, like any cross-user id. Not available to customer-scoped users
// (the scope allowlist admits the read, not the write) or API keys.
func (s *server) revokeOwnSession(w http.ResponseWriter, r *http.Request) {
	if auth.ViaKey(r) {
		http.Error(w, "not available to API keys or the assistant", http.StatusForbidden)
		return
	}
	tag, err := s.st.Pool.Exec(r.Context(),
		`UPDATE user_sessions SET revoked_at=now() WHERE id=$1 AND tenant_id=$2 AND user_id=$3 AND revoked_at IS NULL`,
		r.PathValue("id"), auth.Tenant(r), auth.User(r))
	if err != nil {
		dbErr(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "auth.session_revoked", auth.User(r), map[string]any{"session": r.PathValue("id"), "current": r.PathValue("id") == auth.SessionID(r)})
	w.WriteHeader(204)
}
