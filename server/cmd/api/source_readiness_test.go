package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestIntegrationSourceReadinessDoesNotInventEdgeOptIn(t *testing.T) {
	s, _ := testServer(t)
	tenant := "itest-source-ready"
	seed(t, s, tenant)
	m := http.NewServeMux()
	m.HandleFunc("GET /v1/gateways/{id}/source-readiness", s.sourceReadiness)
	t.Setenv("FLEET_SIGNING_KEY", "")
	path := "/v1/gateways/" + tenant + "-gw/source-readiness"
	w := call(m, tenant, "admin", "GET", path, "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var data struct {
		Ready     bool   `json:"server_ready"`
		Reason    string `json:"reason"`
		SHA       string `json:"desired_sha256"`
		OptIn     string `json:"edge_opt_in"`
		Connected string `json:"connected"`
	}
	json.Unmarshal(w.Body.Bytes(), &data)
	if data.Ready || data.Reason == "" || len(data.SHA) != 64 || data.OptIn != "unknown" || data.Connected != "unknown" {
		t.Fatalf("%+v", data)
	}
	if strings.Contains(w.Body.String(), "devices_yaml") || strings.Contains(w.Body.String(), "ingest_token") {
		t.Fatal("private payload exposed")
	}
	for _, role := range []string{"operator", "viewer", "gateway"} {
		if got := call(m, tenant, role, "GET", path, "").Code; got != 403 {
			t.Fatal(role, got)
		}
	}
	if got := call(m, "foreign", "admin", "GET", path, "").Code; got != 404 {
		t.Fatal("foreign", got)
	}
}
