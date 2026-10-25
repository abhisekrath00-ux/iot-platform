package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestValidateSNMPScan(t *testing.T) {
	for _, p := range []scanParams{{CIDR: "10.0.0.0/24", CommunityEnv: "PLANT_SCAN"}, {CIDR: "192.168.1.9/32", CommunityEnv: "X_1"}} {
		if e := validateScan("snmp", &p); e != nil {
			t.Fatal(e)
		}
	}
	for _, p := range []scanParams{{CIDR: "8.8.8.0/24", CommunityEnv: "X"}, {CIDR: "10.0.0.0/23", CommunityEnv: "X"}, {CIDR: "10.0.0.0/24", CommunityEnv: "secret value"}, {CIDR: "10.0.0.0/24"}, {CIDR: "::1/128", CommunityEnv: "X"}} {
		if validateScan("snmp", &p) == nil {
			t.Fatal("accepted", p)
		}
	}
}
func TestIntegrationSNMPScanSecretAndEnrollmentGates(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-snreq")
	m := http.NewServeMux()
	m.HandleFunc("POST /v1/gateways/{id}/scans", s.requestScan)
	for _, body := range []string{`{"kind":"snmp","params":{"cidr":"10.0.0.0/24","community_env":"X","community":"secret"}}`, `{"kind":"snmp","params":{"cidr":"10.0.0.0/23","community_env":"X"}}`} {
		w := call(m, "itest-snreq", "operator", "POST", "/v1/gateways/itest-snreq-gw/scans", body)
		if w.Code != 400 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
	if w := call(m, "itest-snreq", "viewer", "POST", "/v1/gateways/itest-snreq-gw/scans", `{"kind":"snmp","params":{"cidr":"10.0.0.0/24","community_env":"X"}}`); w.Code != 403 {
		t.Fatal(w.Code)
	}
	// With no broker configured, validation/storage still completes before the
	// existing publish failure. Assert no secret value or unknown field stored.
	w := call(m, "itest-snreq", "operator", "POST", "/v1/gateways/itest-snreq-gw/scans", `{"kind":"snmp","params":{"cidr":"10.0.0.0/24","community_env":"PLANT_SCAN"}}`)
	if w.Code != 502 {
		t.Fatalf("expected local missing-broker failure, got %d %s", w.Code, w.Body)
	}
	var params string
	if e := s.st.Pool.QueryRow(t.Context(), `SELECT params::text FROM gateway_scans WHERE tenant_id='itest-snreq' AND kind='snmp'`).Scan(&params); e != nil || !strings.Contains(params, "PLANT_SCAN") || strings.Contains(params, `"community"`) {
		t.Fatalf("params %s %v", params, e)
	}
	if boundScanConnection("snmp", "snmp", nil, []byte(`{"snmp":[{"Addr":"10.0.0.5"}]}`), map[string]any{"host": "10.0.0.5", "community_env": "X"}) {
		t.Fatal("SNMP enrollment not implemented")
	}
}
