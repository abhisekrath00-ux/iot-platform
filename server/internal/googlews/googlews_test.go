package googlews

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func newSim(t *testing.T, h http.HandlerFunc) (*Provider, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	p, err := NewProvider("cid", "csecret", "https://hex.example/auth/google/callback")
	if err != nil {
		t.Fatal(err)
	}
	p.AuthURL = srv.URL + "/auth"
	p.TokenURL = srv.URL + "/token"
	return p, srv
}

func TestDisabledWithoutClient(t *testing.T) {
	if _, err := NewProvider("", "secret", "https://x/cb"); err != ErrDisabled {
		t.Fatalf("empty client id: %v", err)
	}
	if _, err := NewProvider("id", "", "https://x/cb"); err != ErrDisabled {
		t.Fatalf("empty client secret: %v", err)
	}
}

func TestConsentURL(t *testing.T) {
	p, err := NewProvider("cid", "csecret", "https://hex.example/cb")
	if err != nil {
		t.Fatal(err)
	}
	u := p.ConsentURL("state-123", []string{ScopeOpenID, ScopeEmail, ScopeGmailSend})
	if !strings.HasPrefix(u, "https://accounts.google.com/o/oauth2/v2/auth?") {
		t.Fatalf("default auth endpoint: %s", u)
	}
	q, _ := url.Parse(u)
	v := q.Query()
	if v.Get("access_type") != "offline" {
		t.Fatal("offline access missing - no refresh token would be granted")
	}
	if v.Get("prompt") != "consent" {
		t.Fatal("prompt=consent missing - re-consent would not re-issue a refresh token")
	}
	if v.Get("state") != "state-123" || v.Get("client_id") != "cid" || v.Get("redirect_uri") != "https://hex.example/cb" {
		t.Fatalf("params: %s", u)
	}
	if got := v.Get("scope"); got != "openid email https://www.googleapis.com/auth/gmail.send" {
		t.Fatalf("scope: %q", got)
	}
}

func TestExchangeAgainstSimulator(t *testing.T) {
	p, _ := newSim(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}
		b, _ := io.ReadAll(r.Body)
		f, _ := url.ParseQuery(string(b))
		if f.Get("grant_type") != "authorization_code" || f.Get("code") != "code-1" || f.Get("client_secret") != "csecret" {
			t.Errorf("form: %s", b)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "ya29.sim", "refresh_token": "1//sim-refresh", "expires_in": 3600,
			"scope": "openid email https://www.googleapis.com/auth/gmail.send", "token_type": "Bearer",
		})
	})
	tk, err := p.Exchange(context.Background(), "code-1")
	if err != nil {
		t.Fatal(err)
	}
	if tk.AccessToken != "ya29.sim" || tk.RefreshToken != "1//sim-refresh" {
		t.Fatalf("tokens: %+v", tk)
	}
}

func TestRefreshAgainstSimulator(t *testing.T) {
	p, _ := newSim(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f, _ := url.ParseQuery(string(b))
		if f.Get("grant_type") != "refresh_token" || f.Get("refresh_token") != "1//sim-refresh" {
			t.Errorf("form: %s", b)
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": "ya29.fresh", "expires_in": 3600, "token_type": "Bearer"})
	})
	tk, err := p.Refresh(context.Background(), "1//sim-refresh")
	if err != nil {
		t.Fatal(err)
	}
	if tk.AccessToken != "ya29.fresh" || tk.RefreshToken != "" {
		t.Fatalf("refresh must not invent a refresh token: %+v", tk)
	}
	if _, err := p.Refresh(context.Background(), "  "); err == nil {
		t.Fatal("blank refresh token must fail before any network call")
	}
}

func TestTokenErrorSurfacedWithoutSecrets(t *testing.T) {
	p, _ := newSim(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "Token has been expired or revoked."})
	})
	_, err := p.Exchange(context.Background(), "stale-code")
	if err == nil || !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("want invalid_grant surfaced, got %v", err)
	}
	if strings.Contains(err.Error(), "csecret") {
		t.Fatal("client secret leaked into error")
	}
}
