package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/googlews"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/oidcstate"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/secrets"
)

func gwsReq(t *testing.T, method, path, body, role string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	c := context.WithValue(r.Context(), auth.CtxTenant, "itest-gws")
	c = context.WithValue(c, auth.CtxUser, "itest-gws-admin")
	c = context.WithValue(c, auth.CtxRole, role)
	return r.WithContext(c)
}

func TestGoogleConnectValidation(t *testing.T) {
	p, _ := googlews.NewProvider("cid", "csecret", "https://hex.example/auth/google/callback")
	s := &server{states: oidcstate.NewMemory()}

	// viewer role refused before anything else
	rec := httptest.NewRecorder()
	s.googleConnect(rec, gwsReq(t, "POST", "/v1/integrations/google/connect", `{"surfaces":["gmail_send"]}`, "viewer"))
	if rec.Code != 403 {
		t.Fatalf("viewer: %d", rec.Code)
	}

	// deployment not configured -> 503, no state minted
	rec = httptest.NewRecorder()
	s.googleConnect(rec, gwsReq(t, "POST", "/v1/integrations/google/connect", `{"surfaces":["gmail_send"]}`, "admin"))
	if rec.Code != 503 {
		t.Fatalf("unconfigured: %d", rec.Code)
	}

	s.google = p
	for body, want := range map[string]int{
		`{}`:                         400, // no surfaces
		`{"surfaces":["gmail_all"]}`: 400, // unknown surface
		`not json`:                   400,
	} {
		rec = httptest.NewRecorder()
		s.googleConnect(rec, gwsReq(t, "POST", "/v1/integrations/google/connect", body, "admin"))
		if rec.Code != want {
			t.Fatalf("%s -> %d, want %d", body, rec.Code, want)
		}
	}

	// happy path without DB: stops at audit -> only reachable with a pool, so
	// the full flow is the integration test below.
}

func TestGoogleStatusDefaults(t *testing.T) {
	s := &server{}
	rec := httptest.NewRecorder()
	s.googleStatus(rec, gwsReq(t, "GET", "/v1/integrations/google/status", "", "admin"))
	if rec.Code != 200 {
		t.Fatalf("status: %d", rec.Code)
	}
	var out map[string]any
	json.NewDecoder(rec.Body).Decode(&out)
	if out["configured"] != false || out["connected"] != false {
		t.Fatalf("defaults: %v", out)
	}
}

func TestGoogleCallbackRejectsBadState(t *testing.T) {
	p, _ := googlews.NewProvider("cid", "csecret", "https://hex.example/auth/google/callback")
	s := &server{states: oidcstate.NewMemory(), google: p}
	rec := httptest.NewRecorder()
	s.googleCallback(rec, httptest.NewRequest("GET", "/auth/google/callback?state=bogus&code=x", nil))
	if rec.Code != 403 {
		t.Fatalf("bogus state: %d", rec.Code)
	}
}

// Full connect -> consent -> callback -> status -> disconnect, against a
// Google simulator. Requires TEST_DATABASE_URL (CI); skips locally.
func TestIntegrationGoogleWorkspaceConnection(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-gws")
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 3)
	}
	s.secrets = &secrets.Store{Pool: s.st.Pool, Key: key}
	s.states = oidcstate.NewMemory()
	sim := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "ya29.sim", "refresh_token": "1//sim-rt", "expires_in": 3600, "token_type": "Bearer",
			// Google echoes the granted scopes; the sim grants gmail+calendar but NOT drive
			"scope": "openid email " + googlews.ScopeGmailSend + " " + googlews.ScopeCalendarOnCall,
		})
	}))
	t.Cleanup(sim.Close)
	p, _ := googlews.NewProvider("cid", "csecret", "https://hex.example/auth/google/callback")
	p.TokenURL = sim.URL
	s.google = p
	t.Cleanup(func() {
		s.st.Pool.Exec(context.Background(), `DELETE FROM secrets WHERE tenant_id='itest-gws'`)
		s.st.Pool.Exec(context.Background(), `DELETE FROM audit_log WHERE tenant_id='itest-gws'`)
	})

	// 1. connect: consent URL carries a single-use state
	rec := httptest.NewRecorder()
	s.googleConnect(rec, gwsReq(t, "POST", "/v1/integrations/google/connect", `{"surfaces":["gmail_send","calendar_oncall"]}`, "admin"))
	if rec.Code != 200 {
		t.Fatalf("connect: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		URL string `json:"url"`
	}
	json.NewDecoder(rec.Body).Decode(&out)
	u, err := url.Parse(out.URL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("access_type") != "offline" || !strings.Contains(q.Get("scope"), googlews.ScopeGmailSend) || !strings.Contains(q.Get("scope"), googlews.ScopeCalendarOnCall) {
		t.Fatalf("consent url: %s", out.URL)
	}
	state := q.Get("state")
	if state == "" {
		t.Fatal("no state in consent url")
	}

	// 2. status still disconnected
	rec = httptest.NewRecorder()
	s.googleStatus(rec, gwsReq(t, "GET", "/v1/integrations/google/status", "", "admin"))
	var st map[string]any
	json.NewDecoder(rec.Body).Decode(&st)
	if st["connected"] != false || st["configured"] != true {
		t.Fatalf("status before consent: %v", st)
	}

	// 3. callback: state is single-use
	rec = httptest.NewRecorder()
	s.googleCallback(rec, httptest.NewRequest("GET", "/auth/google/callback?state="+state+"&code=sim-code", nil))
	if rec.Code != 302 {
		t.Fatalf("callback: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	s.googleCallback(rec, httptest.NewRequest("GET", "/auth/google/callback?state="+state+"&code=sim-code", nil))
	if rec.Code != 403 {
		t.Fatalf("state replay must fail: %d", rec.Code)
	}

	// 4. connected now
	rec = httptest.NewRecorder()
	s.googleStatus(rec, gwsReq(t, "GET", "/v1/integrations/google/status", "", "admin"))
	json.NewDecoder(rec.Body).Decode(&st)
	if st["connected"] != true {
		t.Fatalf("status after consent: %v", st)
	}

	// 4b. status reports the granted surfaces from the recorded scope string
	rec = httptest.NewRecorder()
	s.googleStatus(rec, gwsReq(t, "GET", "/v1/integrations/google/status", "", "admin"))
	json.NewDecoder(rec.Body).Decode(&st)
	surf, _ := st["surfaces"].(map[string]any)
	if surf["gmail_send"] != true || surf["calendar_oncall"] != true || surf["drive_reports"] != false {
		t.Fatalf("surfaces after consent: %v", surf)
	}

	// 5. access token mints against the simulator; disconnect closes it
	if _, err := s.googleAccessToken(context.Background(), "itest-gws"); err != nil {
		t.Fatalf("access token: %v", err)
	}
	rec = httptest.NewRecorder()
	s.googleDisconnect(rec, gwsReq(t, "DELETE", "/v1/integrations/google", "", "admin"))
	if rec.Code != 200 {
		t.Fatalf("disconnect: %d", rec.Code)
	}
	if _, err := s.googleAccessToken(context.Background(), "itest-gws"); err == nil {
		t.Fatal("access token after disconnect")
	}
	// disconnect also forgets the recorded scopes
	rec = httptest.NewRecorder()
	s.googleStatus(rec, gwsReq(t, "GET", "/v1/integrations/google/status", "", "admin"))
	json.NewDecoder(rec.Body).Decode(&st)
	if surf, _ := st["surfaces"].(map[string]any); surf["gmail_send"] == true {
		t.Fatalf("surfaces after disconnect: %v", surf)
	}
}
