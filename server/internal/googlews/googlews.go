// Package googlews is the core of the Google Workspace connection: OAuth
// offline-access consent, code exchange and token refresh for the tenant's
// Google surfaces (Gmail send, Calendar on-call sync, Drive/Sheets export).
//
// Air-gapped default: the connection does not exist until an operator sets
// GOOGLE_CLIENT_ID/GOOGLE_CLIENT_SECRET, and every Google endpoint is
// injectable so tests run entirely against a local simulator - this package
// never dials Google on its own in tests. Refresh tokens live in the
// tenant-scoped secrets store (AES-256-GCM at rest), never in this package's
// memory beyond a single call.
package googlews

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Surface scopes, one per agreed Workspace surface. Request only the scopes
// the tenant actually enables - incremental consent keeps the grant minimal.
const (
	ScopeGmailSend       = "https://www.googleapis.com/auth/gmail.send"
	ScopeCalendarOnCall  = "https://www.googleapis.com/auth/calendar.readonly"
	ScopeDriveReportFile = "https://www.googleapis.com/auth/drive.file"
	ScopeOpenID          = "openid"
	ScopeEmail           = "email"
)

var ErrDisabled = errors.New("google workspace connection not configured")

// Provider carries the OAuth client plus every endpoint it talks to. The zero
// endpoints are the real Google ones; tests point them at a simulator.
type Provider struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string

	AuthURL     string // default https://accounts.google.com/o/oauth2/v2/auth
	TokenURL    string // default https://oauth2.googleapis.com/token
	CalendarURL string // default https://www.googleapis.com/calendar/v3
	DriveURL    string // default https://www.googleapis.com/upload/drive/v3/files

	http *http.Client
}

// NewProvider returns ErrDisabled when the operator has not configured a
// Google OAuth client - the air-gapped default is "feature absent".
func NewProvider(clientID, clientSecret, redirectURL string) (*Provider, error) {
	if strings.TrimSpace(clientID) == "" || strings.TrimSpace(clientSecret) == "" {
		return nil, ErrDisabled
	}
	return &Provider{
		ClientID: clientID, ClientSecret: clientSecret, RedirectURL: redirectURL,
		AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token",
		DriveURL: "https://www.googleapis.com/upload/drive/v3/files",
		http:     &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// ConsentURL builds the authorization URL for the given scopes with offline
// access (a refresh token) and incremental consent. state is created and
// verified by the caller (CSRF), exactly as the OIDC login flow does.
func (p *Provider) ConsentURL(state string, scopes []string) string {
	q := url.Values{
		"client_id":              {p.ClientID},
		"redirect_uri":           {p.RedirectURL},
		"response_type":          {"code"},
		"scope":                  {strings.Join(scopes, " ")},
		"access_type":            {"offline"},
		"include_granted_scopes": {"true"},
		"prompt":                 {"consent"}, // guarantee a refresh token on re-consent
		"state":                  {state},
	}
	return p.AuthURL + "?" + q.Encode()
}

// Tokens is the OAuth token response. RefreshToken is present only on a
// consent grant, not on a plain refresh.
type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
}

// Exchange trades the authorization code from the consent redirect for
// tokens. POSTs to the (injectable) token endpoint; never logs secrets.
func (p *Provider) Exchange(ctx context.Context, code string) (*Tokens, error) {
	return p.tokenCall(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {p.RedirectURL},
		"client_id":     {p.ClientID},
		"client_secret": {p.ClientSecret},
	})
}

// Refresh mints a new access token from a stored refresh token.
func (p *Provider) Refresh(ctx context.Context, refreshToken string) (*Tokens, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return nil, errors.New("no refresh token stored for this tenant")
	}
	return p.tokenCall(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {p.ClientID},
		"client_secret": {p.ClientSecret},
	})
}

func (p *Provider) tokenCall(ctx context.Context, form url.Values) (*Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google token call: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		// Google's error body names the reason (invalid_grant etc.) but never
		// contains our secrets; pass the status, not the raw body, upward.
		var ge struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		_ = json.Unmarshal(body, &ge)
		return nil, fmt.Errorf("google token call: %s (%s)", resp.Status, ge.Error)
	}
	var t Tokens
	if err := json.Unmarshal(body, &t); err != nil {
		return nil, fmt.Errorf("google token call: bad response: %w", err)
	}
	if t.AccessToken == "" {
		return nil, errors.New("google token call: empty access token")
	}
	return &t, nil
}
