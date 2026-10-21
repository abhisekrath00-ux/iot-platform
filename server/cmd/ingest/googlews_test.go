package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/googlews"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/notify"
)

// fakeSecretStore is an in-memory googlews.SecretStore keyed by tenant/name.
type fakeSecretStore struct {
	m map[string][]byte
}

func (f *fakeSecretStore) Put(_ context.Context, tenant, name, _ string, v []byte) error {
	if f.m == nil {
		f.m = map[string][]byte{}
	}
	f.m[tenant+"/"+name] = append([]byte(nil), v...)
	return nil
}

func (f *fakeSecretStore) Get(_ context.Context, tenant, name string) ([]byte, error) {
	v, ok := f.m[tenant+"/"+name]
	if !ok {
		return nil, errors.New("not found")
	}
	return v, nil
}

func (f *fakeSecretStore) Delete(_ context.Context, tenant, name string) (bool, error) {
	_, ok := f.m[tenant+"/"+name]
	delete(f.m, tenant+"/"+name)
	return ok, nil
}

func TestGoogleRulesNotifierDisabledWithoutEnv(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "")
	base := notify.FromEnv()
	n, reason := googleRulesNotifier(base, nil)
	if reason != "" {
		t.Fatalf("unexpected reason %q", reason)
	}
	if _, ok := n.(interface {
		Gmail(context.Context, string, string, string, string) error
	}); ok {
		t.Fatal("gmail capability present without GOOGLE_CLIENT_ID")
	}
}

func TestGoogleRulesNotifierNeedsSecretsKey(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "cid")
	t.Setenv("GOOGLE_CLIENT_SECRET", "sec")
	t.Setenv("GOOGLE_REDIRECT_URL", "https://hex.example/auth/google/callback")
	t.Setenv("SECRETS_KEY", "")
	base := notify.FromEnv()
	n, reason := googleRulesNotifier(base, nil)
	if reason == "" {
		t.Fatal("expected a reason when SECRETS_KEY is missing")
	}
	g, ok := n.(interface {
		Gmail(context.Context, string, string, string, string) error
	})
	if !ok {
		t.Fatal("expected the gmail-capable wrapper even without a usable store")
	}
	if err := g.Gmail(context.Background(), "t1", "a@b.co", "s", "b"); err == nil {
		t.Fatal("gmail send should fail closed without a sender")
	}
}

func TestGmailNotifierSendUsesTenantGrant(t *testing.T) {
	var gotAuth, gotRaw string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-1", "token_type": "Bearer", "expires_in": 3600})
		case strings.Contains(r.URL.Path, "/messages/send"):
			gotAuth = r.Header.Get("Authorization")
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode send body: %v", err)
			}
			raw, _ := base64.RawURLEncoding.DecodeString(body["raw"])
			gotRaw = string(raw)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "msg-1"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	p, err := googlews.NewProvider("cid", "sec", "https://hex.example/cb")
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	p.TokenURL = ts.URL + "/token"
	store := &fakeSecretStore{m: map[string][]byte{"t1/" + googlews.RefreshSecretName: []byte("rt-1")}}
	sender := googlews.NewGmailSender(p, store)
	sender.GmailURL = ts.URL + "/gmail/v1/users/me/messages/send"

	g := &gmailNotifier{base: notify.FromEnv(), sender: sender}
	if err := g.Gmail(context.Background(), "t1", "ops@example.com", "Alert: temp", "point temp above 80"); err != nil {
		t.Fatalf("gmail send: %v", err)
	}
	if gotAuth != "Bearer at-1" {
		t.Fatalf("auth header = %q", gotAuth)
	}
	if !strings.Contains(gotRaw, "To: ops@example.com") || !strings.Contains(gotRaw, "Alert: temp") {
		t.Fatalf("raw message missing headers: %q", gotRaw)
	}
}

func TestGmailNotifierUnknownTenantFailsClosed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-1", "token_type": "Bearer", "expires_in": 3600})
	}))
	defer ts.Close()
	p, err := googlews.NewProvider("cid", "sec", "https://hex.example/cb")
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	p.TokenURL = ts.URL + "/token"
	g := &gmailNotifier{base: notify.FromEnv(), sender: googlews.NewGmailSender(p, &fakeSecretStore{m: map[string][]byte{}})}
	if err := g.Gmail(context.Background(), "t9", "ops@example.com", "s", "b"); !errors.Is(err, googlews.ErrNotConnected) {
		t.Fatalf("want ErrNotConnected, got %v", err)
	}
}
