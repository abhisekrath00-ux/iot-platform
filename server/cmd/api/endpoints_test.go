package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func epMux(s *server) http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /v1/system/endpoints", s.listEndpoints)
	m.HandleFunc("POST /v1/system/endpoints", s.addEndpoint)
	m.HandleFunc("POST /v1/system/endpoints/check", s.checkEndpoint)
	m.HandleFunc("DELETE /v1/system/endpoints/{id}", s.deleteEndpoint)
	m.HandleFunc("POST /v1/enrollment/tokens", s.mintEnrollmentToken)
	return m
}

func TestEndpointURLRules(t *testing.T) {
	good := map[string]string{"https://hub.example.com": "https://hub.example.com", "http://10.0.0.5:8000/": "http://10.0.0.5:8000", "HTTPS://Hub.Example.com:8443": "https://hub.example.com:8443"}
	for in, want := range good {
		if got, err := normalizeEndpointURL(in); err != nil || got != want {
			t.Errorf("%q -> %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "hub.example.com", "ftp://x", "http://", "http://u:p@h", "http://h/path", "http://h?x=1", "http://h:99999"} {
		if _, err := normalizeEndpointURL(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	for _, lb := range []string{"http://localhost:8000", "http://127.0.0.1", "http://[::1]:8000", "http://0.0.0.0:8000", "http://api.localhost"} {
		if !isLoopbackURL(lb) {
			t.Errorf("%q should be loopback", lb)
		}
	}
	for _, ok := range []string{"http://192.168.1.10:8000", "https://hub.example.com"} {
		if isLoopbackURL(ok) {
			t.Errorf("%q is not loopback", ok)
		}
	}
	if !probeBlocked("http://169.254.169.254") || probeBlocked("http://10.0.0.5") {
		t.Error("probe blocking wrong")
	}
	for _, sg := range suggestAddresses("8000") {
		if isLoopbackURL(sg) {
			t.Errorf("suggestion %q is loopback", sg)
		}
	}
}

func TestIntegrationServerEndpoints(t *testing.T) {
	t.Setenv("API_PUBLIC_URL", "")
	s, _ := testServer(t)
	h := epMux(s)
	seed(t, s, "itest-ep")
	ctx := t.Context()
	s.st.Pool.Exec(ctx, `DELETE FROM server_endpoints WHERE tenant_id='itest-ep'`)
	t.Cleanup(func() { s.st.Pool.Exec(ctx, `DELETE FROM server_endpoints WHERE tenant_id='itest-ep'`) })

	if w := call(h, "itest-ep", "operator", "GET", "/v1/system/endpoints", ""); w.Code != 403 {
		t.Fatalf("operator list: %d", w.Code)
	}
	if w := call(h, "itest-ep", "admin", "POST", "/v1/system/endpoints", `{"name":"x","url":"nope"}`); w.Code != 400 {
		t.Fatalf("bad url: %d", w.Code)
	}
	if w := call(h, "itest-ep", "admin", "POST", "/v1/system/endpoints", `{"name":"x","url":"http://h","site_id":"no-such-site"}`); w.Code != 400 {
		t.Fatalf("unknown site: %d", w.Code)
	}
	// nothing configured: warning, no addresses
	w := call(h, "itest-ep", "admin", "GET", "/v1/system/endpoints", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "No server address is configured") {
		t.Fatalf("empty: %d %s", w.Code, w.Body.String())
	}
	// loopback is accepted but flagged
	w = call(h, "itest-ep", "admin", "POST", "/v1/system/endpoints", `{"name":"dev","url":"http://localhost:8000"}`)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"loopback":true`) {
		t.Fatalf("loopback add: %d %s", w.Code, w.Body.String())
	}
	w = call(h, "itest-ep", "admin", "POST", "/v1/system/endpoints", `{"name":"primary","url":"https://hub.example.com","priority":10}`)
	var added struct{ ID string }
	json.Unmarshal(w.Body.Bytes(), &added)
	call(h, "itest-ep", "admin", "POST", "/v1/system/endpoints", `{"name":"backup","url":"http://10.1.1.5:8000","priority":50}`)
	w = call(h, "itest-ep", "admin", "GET", "/v1/system/endpoints", "")
	var got struct {
		Resolved []string `json:"resolved"`
		Warnings []string `json:"warnings"`
	}
	json.Unmarshal(w.Body.Bytes(), &got)
	if len(got.Resolved) != 3 || got.Resolved[0] != "https://hub.example.com" || got.Resolved[1] != "http://10.1.1.5:8000" || len(got.Warnings) != 0 {
		t.Fatalf("resolution order: %+v", got)
	}
	if w := call(h, "itest-ep", "admin", "DELETE", "/v1/system/endpoints/"+added.ID, ""); w.Code != 204 {
		t.Fatalf("delete: %d", w.Code)
	}
	if w := call(h, "itest-ep", "admin", "DELETE", "/v1/system/endpoints/"+added.ID, ""); w.Code != 404 {
		t.Fatalf("delete twice: %d", w.Code)
	}
}

func TestIntegrationEnrollmentUsesConfiguredAddress(t *testing.T) {
	t.Setenv("API_PUBLIC_URL", "")
	s, _ := testServer(t)
	h := epMux(s)
	seed(t, s, "itest-ep2")
	ctx := t.Context()
	s.st.Pool.Exec(ctx, `DELETE FROM server_endpoints WHERE tenant_id='itest-ep2'`)
	t.Cleanup(func() { s.st.Pool.Exec(ctx, `DELETE FROM server_endpoints WHERE tenant_id='itest-ep2'`) })
	var site string
	s.st.Pool.QueryRow(ctx, `SELECT id FROM sites WHERE tenant_id='itest-ep2' LIMIT 1`).Scan(&site)
	if site == "" {
		t.Skip("seed made no site")
	}
	mint := func(serial string) map[string]any {
		w := call(h, "itest-ep2", "admin", "POST", "/v1/enrollment/tokens", `{"site_id":"`+site+`","serial":"`+serial+`"}`)
		if w.Code != 201 {
			t.Fatalf("mint: %d %s", w.Code, w.Body.String())
		}
		var m map[string]any
		json.Unmarshal(w.Body.Bytes(), &m)
		return m
	}
	m := mint("EP-1-" + uuid.NewString()[:8])
	if ws, _ := m["warnings"].([]any); len(ws) == 0 || m["server_url"] != "http://localhost:8000" {
		t.Fatalf("expected a loopback warning with nothing configured: %v", m)
	}
	call(h, "itest-ep2", "admin", "POST", "/v1/system/endpoints", `{"name":"hub","url":"https://hub.example.com","priority":10}`)
	call(h, "itest-ep2", "admin", "POST", "/v1/system/endpoints", `{"name":"lan","url":"http://10.1.1.5:8000","priority":20}`)
	m = mint("EP-2-" + uuid.NewString()[:8])
	fb, _ := m["fallback_urls"].([]any)
	if m["server_url"] != "https://hub.example.com" || len(fb) != 1 || fb[0] != "http://10.1.1.5:8000" || m["address_source"] != "workspace setting" {
		t.Fatalf("configured address not used: %v", m)
	}
	if ws, _ := m["warnings"].([]any); len(ws) != 0 {
		t.Fatalf("unexpected warnings: %v", ws)
	}
}

func TestEndpointCheckReachability(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer ok.Close()
	s, _ := testServer(t)
	h := epMux(s)
	seed(t, s, "itest-ep3")
	w := call(h, "itest-ep3", "admin", "POST", "/v1/system/endpoints/check", `{"url":"`+ok.URL+`"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("reachable: %d %s", w.Code, w.Body.String())
	}
	w = call(h, "itest-ep3", "admin", "POST", "/v1/system/endpoints/check", `{"url":"http://127.0.0.1:1"}`)
	if !strings.Contains(w.Body.String(), `"ok":false`) {
		t.Fatalf("unreachable: %s", w.Body.String())
	}
	w = call(h, "itest-ep3", "admin", "POST", "/v1/system/endpoints/check", `{"url":"http://169.254.169.254"}`)
	if !strings.Contains(w.Body.String(), "not checked") {
		t.Fatalf("metadata: %s", w.Body.String())
	}
}
