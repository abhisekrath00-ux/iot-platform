package googlews

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func gmailSim(t *testing.T, check func(authHeader, raw string)) (*GmailSender, *fakeSecrets) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			json.NewEncoder(w).Encode(map[string]any{"access_token": "ya29.t", "expires_in": 3600, "token_type": "Bearer"})
		default: // gmail send
			var req struct {
				Raw string `json:"raw"`
			}
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &req)
			raw, _ := base64.RawURLEncoding.DecodeString(req.Raw)
			check(r.Header.Get("Authorization"), string(raw))
			json.NewEncoder(w).Encode(map[string]string{"id": "msg-1", "threadId": "th-1"})
		}
	}))
	t.Cleanup(srv.Close)
	p, err := NewProvider("cid", "csecret", "https://hex.example/cb")
	if err != nil {
		t.Fatal(err)
	}
	p.TokenURL = srv.URL + "/token"
	f := newFake()
	g := NewGmailSender(p, f)
	g.GmailURL = srv.URL + "/gmail/v1/users/me/messages/send"
	return g, f
}

func TestGmailSendAgainstSimulator(t *testing.T) {
	var gotAuth, gotRaw string
	g, f := gmailSim(t, func(a, raw string) { gotAuth, gotRaw = a, raw })
	ctx := context.Background()
	if err := SaveRefreshToken(ctx, f, "t1", "admin@x", &Tokens{RefreshToken: "1//rt"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveGrantedScopes(ctx, f, "t1", "admin@x", "openid "+ScopeGmailSend); err != nil {
		t.Fatal(err)
	}
	id, err := g.Send(ctx, "t1", "ops@example.com", "Alert: temp high", "Line1\nLine2")
	if err != nil || id != "msg-1" {
		t.Fatalf("send: %q %v", id, err)
	}
	if gotAuth != "Bearer ya29.t" {
		t.Fatalf("auth header: %q", gotAuth)
	}
	for _, want := range []string{"To: ops@example.com", "Subject: Alert: temp high", "Line1\nLine2"} {
		if !strings.Contains(gotRaw, want) {
			t.Fatalf("raw message missing %q:\n%s", want, gotRaw)
		}
	}
}

func TestGmailSendFailsClosedWhenNotConnected(t *testing.T) {
	g, f := gmailSim(t, func(_, _ string) { t.Error("simulator must not be called") })
	// granted scopes on record but no refresh token: still fails closed
	if err := SaveGrantedScopes(context.Background(), f, "t9", "admin@x", "openid "+ScopeGmailSend); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Send(context.Background(), "t9", "ops@example.com", "s", "b"); err != ErrNotConnected {
		t.Fatalf("want ErrNotConnected, got %v", err)
	}
}

func TestGmailSendRefusedOnPartialOrLegacyGrant(t *testing.T) {
	g, f := gmailSim(t, func(_, _ string) { t.Error("simulator must not be called") })
	ctx := context.Background()
	if err := SaveRefreshToken(ctx, f, "t1", "admin@x", &Tokens{RefreshToken: "1//rt"}); err != nil {
		t.Fatal(err)
	}
	// legacy connection: no scope record at all
	if _, err := g.Send(ctx, "t1", "ops@example.com", "s", "b"); err != ErrScopeUnknown {
		t.Fatalf("legacy grant: want ErrScopeUnknown, got %v", err)
	}
	// partial grant: calendar only, no gmail.send
	if err := SaveGrantedScopes(ctx, f, "t1", "admin@x", "openid "+ScopeCalendarOnCall); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Send(ctx, "t1", "ops@example.com", "s", "b"); !errors.Is(err, ErrScopeNotGranted) {
		t.Fatalf("partial grant: want ErrScopeNotGranted, got %v", err)
	}
}

func TestRFC2822HeaderSafety(t *testing.T) {
	g, _ := gmailSim(t, func(_, _ string) { t.Error("simulator must not be called") })
	if _, err := g.Send(context.Background(), "t1", "a@b.com\r\nBcc: evil@x", "ok", "body"); err == nil {
		t.Fatal("CRLF recipient accepted")
	}
	raw := buildRFC2822("a@b.com", "Température haute", "corps")
	if !strings.Contains(raw, "Subject: =?utf-8?") {
		t.Fatalf("non-ASCII subject not encoded:\n%s", raw)
	}
}

func TestGmailBadRecipient(t *testing.T) {
	g, _ := gmailSim(t, func(_, _ string) { t.Error("simulator must not be called") })
	if _, err := g.Send(context.Background(), "t1", "not-an-address", "s", "b"); err == nil {
		t.Fatal("bad recipient accepted")
	}
}
