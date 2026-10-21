package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/googlews"
)

// Google Workspace connection routes. Admin-only, tenant-scoped; the consent
// callback is unauthenticated but bound to a single-use state minted by
// connect (same pattern as OIDC SSO). Customer-scoped users are refused by
// the scopedAllows middleware like every unlisted route. No route dials
// Google unless the operator configured GOOGLE_CLIENT_ID/SECRET.

// googleSurfaces maps the surface names the UI sends to their OAuth scopes.
var googleSurfaces = map[string]string{
	"gmail_send":      googlews.ScopeGmailSend,
	"calendar_oncall": googlews.ScopeCalendarOnCall,
	"drive_reports":   googlews.ScopeDriveReportFile,
}

// GET /v1/integrations/google/status (admin): is the deployment configured
// (OAuth client present) and is THIS tenant connected (refresh token stored)?
func (s *server) googleStatus(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	out := map[string]any{"configured": s.google != nil, "connected": false, "surfaces": map[string]any{}}
	if s.secrets != nil && len(s.secrets.Key) > 0 {
		tenant := auth.Tenant(r)
		if infos, err := s.secrets.List(r.Context(), tenant); err == nil {
			for _, i := range infos {
				if i.Name == googlews.RefreshSecretName {
					out["connected"] = true
				}
			}
		}
		// Granted surfaces from the scope string recorded at consent. "unknown"
		// marks connections that predate scope recording - the honest state is
		// "reconnect to confirm", not a guess.
		surfaces := map[string]any{}
		if recorded, ok := googlews.LoadGrantedScopes(r.Context(), s.secrets, tenant); ok {
			for name, scope := range googleSurfaces {
				surfaces[name] = googlews.ScopeGranted(recorded, scope)
			}
		} else {
			for name := range googleSurfaces {
				surfaces[name] = "unknown"
			}
		}
		out["surfaces"] = surfaces
	}
	writeJSON(w, 200, out)
}

// POST /v1/integrations/google/connect (admin): body {"surfaces": ["gmail_send", ...]}.
// Returns the consent URL; the admin opens it, grants, and Google redirects
// to the callback. State is single-use, 10-minute TTL, and carries the
// tenant + user + surfaces so the callback needs no session.
func (s *server) googleConnect(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	if s.google == nil {
		writeJSON(w, 503, map[string]any{"error": "google workspace not configured on this deployment (GOOGLE_CLIENT_ID/SECRET)"})
		return
	}
	var in struct {
		Surfaces []string `json:"surfaces"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.Surfaces) == 0 {
		http.Error(w, "body: {\"surfaces\": [\"gmail_send\"|\"calendar_oncall\"|\"drive_reports\", ...]}", 400)
		return
	}
	scopes := []string{googlews.ScopeOpenID, googlews.ScopeEmail}
	granted := []string{}
	for _, sf := range in.Surfaces {
		sc, ok := googleSurfaces[sf]
		if !ok {
			http.Error(w, "unknown surface: "+sf, 400)
			return
		}
		scopes = append(scopes, sc)
		granted = append(granted, sf)
	}
	state, err := auth.RandomToken()
	if err != nil {
		dbErr(w, err)
		return
	}
	payload := auth.Tenant(r) + "|" + auth.User(r) + "|" + strings.Join(granted, ",")
	if err := s.states.Put(state, payload, 10*time.Minute); err != nil {
		dbErr(w, err)
		return
	}
	s.audit(r, "google.connect_start", auth.User(r), map[string]any{"surfaces": granted})
	writeJSON(w, 200, map[string]any{"url": s.google.ConsentURL(state, scopes)})
}

// GET /auth/google/callback (public, state-bound): exchanges the code and
// stores the tenant's refresh token in the secrets store.
func (s *server) googleCallback(w http.ResponseWriter, r *http.Request) {
	if s.google == nil {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	state, code := q.Get("state"), q.Get("code")
	payload, ok := s.states.Take(state) // single use, TTL enforced by the store
	if !ok || code == "" {
		http.Error(w, "invalid or expired state", 403)
		return
	}
	parts := strings.SplitN(payload, "|", 3)
	if len(parts) != 3 || s.secrets == nil || len(s.secrets.Key) == 0 {
		http.Error(w, "connection state invalid or secrets store unavailable", 500)
		return
	}
	tenant, user := parts[0], parts[1]
	tk, err := s.google.Exchange(r.Context(), code)
	if err != nil {
		http.Error(w, "google exchange failed", 403)
		return
	}
	if err := googlews.SaveRefreshToken(r.Context(), s.secrets, tenant, user, tk); err != nil {
		http.Error(w, "google grant carried no refresh token; retry the consent", 502)
		return
	}
	if err := googlews.SaveGrantedScopes(r.Context(), s.secrets, tenant, user, tk.Scope); err != nil {
		http.Error(w, "google grant recorded without its scope list; retry the consent", 502)
		return
	}
	s.audit(r, "google.connected", user, map[string]any{"tenant": tenant, "surfaces": parts[2]})
	http.Redirect(w, r, "/settings?google=connected", http.StatusFound)
}

// DELETE /v1/integrations/google (admin): forget this tenant's grant. Surface
// calls then fail closed (ErrNotConnected) until a new consent.
func (s *server) googleDisconnect(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	if s.secrets != nil && len(s.secrets.Key) > 0 {
		if err := googlews.Disconnect(r.Context(), s.secrets, auth.Tenant(r)); err != nil {
			dbErr(w, err)
			return
		}
	}
	s.audit(r, "google.disconnected", auth.User(r), nil)
	writeJSON(w, 200, map[string]any{"connected": false})
}

// gmailTestNotifier adds the Gmail capability to the channel-test path: the
// base notifier covers the other types; Gmail binds the request's tenant so
// the test exercises exactly the tenant's own grant.
type gmailTestNotifier struct {
	base   rulesNotifier
	s      *server
	tenant string
}

type rulesNotifier interface {
	Email(ctx context.Context, to []string, subject, body string) error
	Slack(ctx context.Context, channel, text string) error
}

func (g *gmailTestNotifier) Email(ctx context.Context, to []string, subject, body string) error {
	return g.base.Email(ctx, to, subject, body)
}

func (g *gmailTestNotifier) Slack(ctx context.Context, channel, text string) error {
	return g.base.Slack(ctx, channel, text)
}

func (g *gmailTestNotifier) Gmail(ctx context.Context, tenant, to, subject, body string) error {
	if g.s.google == nil {
		return errors.New("google workspace not configured on this deployment")
	}
	_, err := googlews.NewGmailSender(g.s.google, g.s.secrets).Send(ctx, tenant, to, subject, body)
	return err
}

// googleAccessToken mints an access token for one call batch; surfaces use it.
func (s *server) googleAccessToken(ctx context.Context, tenant string) (string, error) {
	return googlews.AccessToken(ctx, s.google, s.secrets, tenant)
}
