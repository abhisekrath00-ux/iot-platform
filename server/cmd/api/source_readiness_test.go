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

func TestIntegrationSourceReadinessApplySafety(t *testing.T) {
	for _, mode := range []string{"missing-guard", "missing-store", "empty-safe", "unresolved", "terminal", "missing-db-history", "missing-retained-history"} {
		t.Run(mode, func(t *testing.T) {
			s, m, tenant, path := applyGuardFixture(t)
			s.applySafetyCandidate = true
			if mode != "missing-guard" {
				if err := installApplyGuard(t, s); err != nil {
					t.Fatal(err)
				}
			}
			if mode != "missing-store" {
				s.applyAttemptFence = testApplyFence(t)
			}
			m.HandleFunc("GET /v1/gateways/{id}/source-readiness", s.sourceReadiness)
			var publications int
			s.opsPublishHook = func(string, []byte) error { publications++; return nil }
			if mode == "unresolved" || mode == "terminal" || mode == "missing-db-history" || mode == "missing-retained-history" {
				first := call(m, tenant, "admin", "POST", path, `{}`)
				if first.Code != 202 {
					t.Fatal(first.Code, first.Body.String())
				}
				if mode == "terminal" || mode == "missing-retained-history" {
					if _, err := s.st.Pool.Exec(t.Context(), `UPDATE gateway_config_pushes SET status='rejected'`); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "missing-retained-history" {
					s.applyAttemptFence = testApplyFence(t)
				}
				if mode == "missing-db-history" {
					if _, err := s.st.Pool.Exec(t.Context(), `ALTER TABLE gateway_config_pushes DISABLE TRIGGER apply_attempt_guard_candidate;DELETE FROM gateway_config_pushes;ALTER TABLE gateway_config_pushes ENABLE TRIGGER apply_attempt_guard_candidate`); err != nil {
						t.Fatal(err)
					}
				}
			}
			before := publications
			readyPath := strings.TrimSuffix(path, "config-push") + "source-readiness"
			w := call(m, tenant, "admin", "GET", readyPath, "")
			var data struct {
				Ready     bool   `json:"server_ready"`
				Reason    string `json:"reason"`
				Safety    string `json:"apply_safety_reason"`
				OptIn     string `json:"edge_opt_in"`
				Connected string `json:"connected"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &data) != nil {
				t.Fatal(w.Code, w.Body.String())
			}
			expected := map[string]string{"missing-guard": "safety_guard_unavailable", "missing-store": "safety_store_unavailable", "empty-safe": "", "unresolved": "gateway_response_unresolved", "terminal": "", "missing-db-history": "database_history_missing", "missing-retained-history": "retained_history_missing"}[mode]
			if data.Safety != expected || data.Ready != (expected == "") || data.OptIn != "unknown" || data.Connected != "unknown" || publications != before || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(data, expected, publications, before, w.Body.String())
			}
			if expected != "" && data.Reason == "" {
				t.Fatal("unsafe readiness unexplained")
			}
			for _, role := range []string{"operator", "viewer", "gateway"} {
				if got := call(m, tenant, role, "GET", readyPath, "").Code; got != 403 {
					t.Fatal(role, got)
				}
			}
			if got := call(m, "foreign", "admin", "GET", readyPath, "").Code; got != 404 {
				t.Fatal("foreign", got)
			}
		})
	}
}
