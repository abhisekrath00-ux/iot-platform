package googlews

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeSecrets struct {
	m    map[string][]byte
	puts int
}

func newFake() *fakeSecrets { return &fakeSecrets{m: map[string][]byte{}} }
func (f *fakeSecrets) Put(_ context.Context, tenant, name, _ string, v []byte) error {
	f.puts++
	f.m[tenant+"/"+name] = append([]byte(nil), v...)
	return nil
}
func (f *fakeSecrets) Get(_ context.Context, tenant, name string) ([]byte, error) {
	v, ok := f.m[tenant+"/"+name]
	if !ok {
		return nil, errFakeMissing
	}
	return v, nil
}
func (f *fakeSecrets) Delete(_ context.Context, tenant, name string) (bool, error) {
	k := tenant + "/" + name
	_, ok := f.m[k]
	delete(f.m, k)
	return ok, nil
}

var errFakeMissing = errorString("missing")

type errorString string

func (e errorString) Error() string { return string(e) }

func TestSaveLoadDisconnect(t *testing.T) {
	ctx := context.Background()
	f := newFake()
	tk := &Tokens{AccessToken: "a", RefreshToken: "1//rt"}
	if err := SaveRefreshToken(ctx, f, "t1", "admin@x", tk); err != nil {
		t.Fatal(err)
	}
	rt, err := LoadRefreshToken(ctx, f, "t1")
	if err != nil || rt != "1//rt" {
		t.Fatalf("load: %q %v", rt, err)
	}
	// tenant isolation: another tenant sees not-connected
	if _, err := LoadRefreshToken(ctx, f, "t2"); err != ErrNotConnected {
		t.Fatalf("t2: %v", err)
	}
	// a plain-refresh response (no refresh token) must never overwrite
	if err := SaveRefreshToken(ctx, f, "t1", "admin@x", &Tokens{AccessToken: "b"}); err == nil {
		t.Fatal("empty refresh token accepted")
	}
	if rt, _ := LoadRefreshToken(ctx, f, "t1"); rt != "1//rt" {
		t.Fatalf("stored token clobbered: %q", rt)
	}
	if err := Disconnect(ctx, f, "t1"); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRefreshToken(ctx, f, "t1"); err != ErrNotConnected {
		t.Fatalf("after disconnect: %v", err)
	}
}

func TestAccessTokenEndToEndAgainstSimulator(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"access_token": "ya29.live-call", "expires_in": 3600, "token_type": "Bearer"})
	}))
	t.Cleanup(srv.Close)
	p, err := NewProvider("cid", "csecret", "https://hex.example/cb")
	if err != nil {
		t.Fatal(err)
	}
	p.TokenURL = srv.URL
	f := newFake()
	ctx := context.Background()
	if _, err := AccessToken(ctx, p, f, "t1"); err != ErrNotConnected {
		t.Fatalf("unconnected tenant must fail closed: %v", err)
	}
	if err := SaveRefreshToken(ctx, f, "t1", "admin@x", &Tokens{AccessToken: "a", RefreshToken: "1//rt"}); err != nil {
		t.Fatal(err)
	}
	at, err := AccessToken(ctx, p, f, "t1")
	if err != nil || at != "ya29.live-call" {
		t.Fatalf("access token: %q %v", at, err)
	}
}
