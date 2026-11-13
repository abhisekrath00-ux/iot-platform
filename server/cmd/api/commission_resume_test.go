package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestIntegrationCommissionRecoveryIsScopedMetadata(t *testing.T) {
	s, _ := testServer(t)
	tenant := "itest-resume"
	seed(t, s, tenant)
	seed(t, s, "itest-resume-other")
	m := http.NewServeMux()
	m.HandleFunc("POST /v1/commissioning/sessions", s.createCommissionSession)
	m.HandleFunc("GET /v1/commissioning/sessions", s.listCommissionSessions)
	for _, owner := range []string{tenant, "itest-resume-other"} {
		limit := 1
		if owner == tenant {
			limit = 52
		}
		for i := 0; i < limit; i++ {
			w := call(m, owner, "admin", "POST", "/v1/commissioning/sessions", fmt.Sprintf(`{"site_id":%q,"serial":%q}`, owner+"-site", fmt.Sprintf("%s-%d", owner, i)))
			if w.Code != 201 {
				t.Fatalf("seed %d %s", w.Code, w.Body)
			}
		}
	}
	var before int
	s.st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM gateways WHERE tenant_id=$1`, tenant).Scan(&before)
	for _, role := range []string{"admin", "operator"} {
		w := call(m, tenant, role, "GET", "/v1/commissioning/sessions", "")
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable recovery")
		}
		var rows []map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil || len(rows) != 50 {
			t.Fatalf("%d %v", len(rows), err)
		}
		for _, row := range rows {
			if !strings.HasPrefix(row["serial"].(string), tenant+"-") || strings.Contains(row["serial"].(string), "other") {
				t.Fatal("tenant leak")
			}
			if len(row) != 6 {
				t.Fatalf("unexpected fields %v", row)
			}
		}
		for _, secret := range []string{"claim_code", "enroll_string", "qr_payload", "ingest_token"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatal("secret metadata", secret)
			}
		}
	}
	for _, role := range []string{"viewer", "gateway"} {
		if w := call(m, tenant, role, "GET", "/v1/commissioning/sessions", ""); w.Code != 403 {
			t.Fatal("unauthorized role", role, w.Code)
		}
	}
	w := call(m, "unknown-recovery-tenant", "admin", "GET", "/v1/commissioning/sessions", "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("empty %d %s", w.Code, w.Body)
	}
	var after int
	s.st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM gateways WHERE tenant_id=$1`, tenant).Scan(&after)
	if before != after {
		t.Fatal("recovery mutated gateways")
	}
}

func TestIntegrationResumePreservesRequestedProbeBeforeReply(t *testing.T) {
	s, _ := testServer(t)
	tenant := "itest-resume-probe"
	seed(t, s, tenant)
	m := http.NewServeMux()
	m.HandleFunc("POST /v1/commissioning/sessions", s.createCommissionSession)
	m.HandleFunc("GET /v1/commissioning/sessions/{id}", s.getCommissionSession)
	w := call(m, tenant, "admin", "POST", "/v1/commissioning/sessions", `{"site_id":"itest-resume-probe-site","serial":"probe-resume"}`)
	if w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var session map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	id := session["session_id"].(string)
	_, err := s.st.Pool.Exec(t.Context(), `UPDATE commissioning_sessions SET port_test=$1 WHERE id=$2`, `{"requested_at":"2026-11-12T06:00:00Z","config":{"port":"COM7","baud":9600,"data_bits":8,"stop_bits":1,"parity":"even","address":17,"func":3,"register":3048,"count":2,"type":"f32","word_order":"cdab","timeout_ms":1500}}`, id)
	if err != nil {
		t.Fatal(err)
	}
	w = call(m, tenant, "admin", "GET", "/v1/commissioning/sessions/"+id, "")
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var saved struct {
		PortTest struct {
			Config map[string]any `json:"config"`
			Result any            `json:"result"`
		} `json:"port_test"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.PortTest.Config["port"] != "COM7" || saved.PortTest.Config["parity"] != "even" || saved.PortTest.Result != nil || saved.State != "awaiting_claim" {
		t.Fatalf("unexpected recovery %+v", saved)
	}
	if w = call(m, "itest-resume-probe-other", "admin", "GET", "/v1/commissioning/sessions/"+id, ""); w.Code != 404 {
		t.Fatal("cross tenant", w.Code)
	}
}

func TestIntegrationResumeSourceConnectionIsScopedAndFiltered(t *testing.T) {
	s, _ := testServer(t)
	tenant := "itest-source-resume"
	seed(t, s, tenant)
	_, err := s.st.Pool.Exec(t.Context(), `UPDATE devices SET profile='modbus-generic',name='Saved meter',config=$1 WHERE id=$2`, `{"private_diagnostics":"do-not-return","connection":{"port":"COM7","baud":9600,"data_bits":8,"stop_bits":1,"parity":"even","address":17,"interval_seconds":10,"password":"do-not-return"}}`, tenant+"-dev")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.st.Pool.Exec(t.Context(), `INSERT INTO commissioning_sessions(id,tenant_id,site_id,gateway_id,serial,device_id,created_by) VALUES($1,$2,$3,$4,$5,$6,'test-user')`, "source-resume", tenant, tenant+"-site", tenant+"-gw", "SER-"+tenant, tenant+"-dev")
	if err != nil {
		t.Fatal(err)
	}
	m := http.NewServeMux()
	m.HandleFunc("GET /v1/commissioning/sessions/{id}", s.getCommissionSession)
	for _, role := range []string{"admin", "operator"} {
		w := call(m, tenant, role, "GET", "/v1/commissioning/sessions/source-resume", "")
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable source")
		}
		var data struct {
			Source struct {
				Name, Driver string
				Connection   map[string]any
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		if data.Source.Name != "Saved meter" || data.Source.Driver != "modbus-generic" || data.Source.Connection["parity"] != "even" || data.Source.Connection["port"] != "COM7" {
			t.Fatalf("%+v", data)
		}
		if strings.Contains(w.Body.String(), "do-not-return") || strings.Contains(w.Body.String(), "password") {
			t.Fatal("arbitrary config leaked")
		}
	}
	for _, role := range []string{"viewer", "gateway"} {
		if w := call(m, tenant, role, "GET", "/v1/commissioning/sessions/source-resume", ""); w.Code != 403 {
			t.Fatal(role, w.Code)
		}
	}
	if w := call(m, "wrong-source-tenant", "admin", "GET", "/v1/commissioning/sessions/source-resume", ""); w.Code != 404 {
		t.Fatal("tenant leak", w.Code)
	}
	// A stale linkage must not silently show default settings or another gateway's source.
	_, err = s.st.Pool.Exec(t.Context(), `UPDATE commissioning_sessions SET gateway_id=$1 WHERE id='source-resume'`, "missing-gateway")
	if err == nil {
		t.Fatal("expected FK guard")
	}
}
