package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/oidcstate"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
	"github.com/golang-jwt/jwt/v5"
)

// Local synthetic issuer: no external provider, user account or OAuth grant.
func TestIntegrationOIDCCrossReplicaCallback(t *testing.T) {
	a, _ := testServer(t)
	seed(t, a, "itest-oidcreplica")
	st, err := store.Connect(t.Context(), os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	b := &server{st: st, secret: []byte("synthetic-shared-signing-secret")}
	a.secret = b.secret
	a.states = oidcstate.NewPostgres(a.st.Pool)
	b.states = oidcstate.NewPostgres(b.st.Pool)
	if _, err := a.st.Pool.Exec(t.Context(), `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('oidc-replica-user','itest-oidcreplica','oidc-replica@example.invalid','Synthetic','viewer') ON CONFLICT(id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var exchanges atomic.Int32
	var codes sync.Map
	mux := http.NewServeMux()
	issuer := httptest.NewServer(mux)
	t.Cleanup(issuer.Close)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"issuer": issuer.URL, "authorization_endpoint": issuer.URL + "/authorize", "token_endpoint": issuer.URL + "/token", "jwks_uri": issuer.URL + "/keys"})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "replica-key", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		exchanges.Add(1)
		r.ParseForm()
		code := r.Form.Get("code")
		v, ok := codes.LoadAndDelete(code)
		if !ok || r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("client_id") != "synthetic-client" {
			http.Error(w, "invalid code", 400)
			return
		}
		nonce := v.(string)
		email := "oidc-replica@example.invalid"
		if strings.HasPrefix(code, "unknown-") {
			email = "unregistered@example.invalid"
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": issuer.URL, "aud": "synthetic-client", "sub": "synthetic-subject", "email": email, "nonce": nonce, "exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Unix()})
		tok.Header["kid"] = "replica-key"
		signed, e := tok.SignedString(key)
		if e != nil {
			http.Error(w, "signing failed", 500)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"id_token": signed})
	})
	for _, s := range []*server{a, b} {
		s.oidc, err = auth.NewOIDCProvider(context.Background(), issuer.URL, "synthetic-client", "", "http://platform.example.invalid/auth/oidc/callback")
		if err != nil {
			t.Fatal(err)
		}
	}
	login := func(code string) (string, string) {
		t.Helper()
		w := httptest.NewRecorder()
		a.oidcLogin(w, httptest.NewRequest("GET", "/auth/oidc/login", nil))
		if w.Code != 302 {
			t.Fatal(w.Code, w.Body.String())
		}
		u, e := url.Parse(w.Header().Get("Location"))
		if e != nil {
			t.Fatal(e)
		}
		state, nonce := u.Query().Get("state"), u.Query().Get("nonce")
		if state == "" || nonce == "" {
			t.Fatal("missing state/nonce")
		}
		codes.Store(code, nonce)
		return state, nonce
	}
	callback := func(s *server, state, code string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.oidcCallback(w, httptest.NewRequest("GET", "/auth/oidc/callback?"+url.Values{"state": {state}, "code": {code}}.Encode(), nil))
		return w
	}
	state, _ := login("good")
	w := callback(b, state, "good")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var response struct {
		Token  string `json:"token"`
		UserID string `json:"user_id"`
		Tenant string `json:"tenant_id"`
		Role   string `json:"role"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	var claims auth.Claims
	token, e := jwt.ParseWithClaims(response.Token, &claims, func(tok *jwt.Token) (any, error) { return a.secret, nil }, jwt.WithValidMethods([]string{"HS256"}))
	if e != nil || !token.Valid || claims.Subject != "oidc-replica-user" || claims.TenantID != "itest-oidcreplica" || claims.Role != "viewer" || claims.ID == "" {
		t.Fatal("invalid platform session", e)
	}
	var count int
	if err := a.st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM user_sessions WHERE id=$1 AND user_id='oidc-replica-user' AND tenant_id='itest-oidcreplica'`, claims.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("session not visible on originating pool", err, count)
	}
	before := exchanges.Load()
	if w := callback(a, state, "good"); w.Code != 403 || exchanges.Load() != before {
		t.Fatal("replay reached issuer")
	}
	state, _ = login("race")
	var wins atomic.Int32
	var wg sync.WaitGroup
	before = exchanges.Load()
	for i := range 16 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 1 {
				s = b
			}
			w := callback(s, state, "race")
			if w.Code == 200 {
				wins.Add(1)
			} else if w.Code != 403 {
				t.Errorf("race status %d", w.Code)
			}
		}(i)
	}
	wg.Wait()
	if wins.Load() != 1 || exchanges.Load() != before+1 {
		t.Fatal("non-single-use callback", wins.Load(), exchanges.Load()-before)
	}
	state, _ = login("expired")
	if _, e := a.st.Pool.Exec(t.Context(), `UPDATE oauth_states SET expires_at=clock_timestamp()-interval '1 second' WHERE state_hash=encode(sha256($1::bytea),'hex')`, []byte(state)); e != nil {
		t.Fatal(e)
	}
	before = exchanges.Load()
	if w := callback(b, state, "expired"); w.Code != 403 || exchanges.Load() != before {
		t.Fatal("expired state reached issuer")
	}
	var sessionsBefore int
	if e := a.st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM user_sessions WHERE user_id='oidc-replica-user'`).Scan(&sessionsBefore); e != nil {
		t.Fatal(e)
	}
	state, _ = login("badnonce")
	codes.Store("badnonce", "wrong-nonce")
	if w := callback(b, state, "badnonce"); w.Code != 403 || strings.Contains(w.Body.String(), "token") {
		t.Fatal("nonce accepted")
	}
	before = exchanges.Load()
	if w := callback(a, state, "badnonce"); w.Code != 403 || exchanges.Load() != before {
		t.Fatal("failed exchange state reusable")
	}
	state, _ = login("unknown-user")
	if w := callback(b, state, "unknown-user"); w.Code != 403 {
		t.Fatal("unregistered user accepted")
	}
	var sessionsAfter int
	if e := a.st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM user_sessions WHERE user_id='oidc-replica-user'`).Scan(&sessionsAfter); e != nil || sessionsAfter != sessionsBefore {
		t.Fatal("rejected callbacks created session", e, sessionsBefore, sessionsAfter)
	}
	if e := a.st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM users WHERE email='unregistered@example.invalid'`).Scan(&count); e != nil || count != 0 {
		t.Fatal("SSO auto-provisioned unknown user", e, count)
	}
	state, _ = login("outage")
	b.st.Close()
	before = exchanges.Load()
	if w := callback(b, state, "outage"); w.Code != 403 || exchanges.Load() != before {
		t.Fatal("state outage accepted or issuer called")
	}
	if w := callback(a, state, "outage"); w.Code != 200 {
		t.Fatal("other healthy pool unable to consume unconsumed state", w.Code, w.Body.String())
	}
}
