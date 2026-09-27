package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func fakeProvider(t *testing.T, key *rsa.PrivateKey, mutateClaims func(jwt.MapClaims)) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	sign := func(nonce string) string {
		claims := jwt.MapClaims{
			"iss":   srv.URL,
			"aud":   "client-1",
			"sub":   "user-9",
			"exp":   time.Now().Add(time.Hour).Unix(),
			"iat":   time.Now().Unix(),
			"email": "dev@hexmon.example",
			"name":  "Dev",
			"nonce": nonce,
		}
		if mutateClaims != nil {
			mutateClaims(claims)
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = "k1"
		s, err := tok.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"issuer": srv.URL, "authorization_endpoint": srv.URL + "/auth",
			"token_endpoint": srv.URL + "/token", "jwks_uri": srv.URL + "/jwks",
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") == "" {
			http.Error(w, "bad", 400)
			return
		}
		nonce := "n-ok"
		if mutateClaims != nil {
			nonce = "n-ok" // signed below with mutation
		}
		json.NewEncoder(w).Encode(map[string]string{"id_token": sign(nonce), "access_token": "x"})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		pub := key.Public().(*rsa.PublicKey)
		e := big.NewInt(int64(pub.E)).Bytes()
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1",
			"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(e),
		}}})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newKey(t *testing.T) *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestOIDCFullFlow(t *testing.T) {
	key := newKey(t)
	srv := fakeProvider(t, key, nil)
	p, err := NewOIDCProvider(context.Background(), srv.URL, "client-1", "secret", "http://app/cb")
	if err != nil {
		t.Fatal(err)
	}
	id, err := p.Exchange(context.Background(), "code-1", "n-ok")
	if err != nil {
		t.Fatal(err)
	}
	if id.Email != "dev@hexmon.example" {
		t.Fatalf("email %q", id.Email)
	}
}

func TestOIDCRejectsBadNonceAndIssuer(t *testing.T) {
	key := newKey(t)
	srv := fakeProvider(t, key, nil)
	p, _ := NewOIDCProvider(context.Background(), srv.URL, "client-1", "", "http://app/cb")
	if _, err := p.Exchange(context.Background(), "code-1", "n-wrong"); err == nil {
		t.Fatal("nonce mismatch accepted")
	}
	// wrong issuer inside the signed token
	srv2 := fakeProvider(t, key, func(c jwt.MapClaims) { c["iss"] = "https://evil.example" })
	p2, err := NewOIDCProvider(context.Background(), srv2.URL, "client-1", "", "http://app/cb")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p2.Exchange(context.Background(), "code-1", "n-ok"); err == nil {
		t.Fatal("id_token issuer mismatch accepted")
	}
}

func TestOIDCDisabledWithoutIssuer(t *testing.T) {
	if _, err := NewOIDCProvider(context.Background(), "", "c", "", "r"); err != ErrOIDCDisabled {
		t.Fatalf("got %v", err)
	}
}

func TestRandomTokenEntropy(t *testing.T) {
	a, _ := RandomToken()
	b, _ := RandomToken()
	if a == b || len(a) < 32 {
		t.Fatal("tokens weak")
	}
}
