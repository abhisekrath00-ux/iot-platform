package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestIntegrationInvestigate(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-inv")
	seed(t, s, "itest-inv2")
	ctx := t.Context()
	pool := s.st.Pool
	for _, q := range []string{
		`UPDATE sites SET name='North plant' WHERE id='itest-inv-site'`,
		`DELETE FROM alerts WHERE tenant_id='itest-inv'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM alerts WHERE tenant_id='itest-inv'`) })
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/diagnostics/investigate", s.investigate)
	get := func(tenant, role, scope string) (int, map[string]any) {
		w := call(mux, tenant, role, "GET", "/v1/diagnostics/investigate?scope="+scope, "")
		var o map[string]any
		json.Unmarshal(w.Body.Bytes(), &o)
		return w.Code, o
	}
	problems := func(o map[string]any) string {
		b, _ := json.Marshal(o["report"])
		return string(b)
	}

	// healthy: fresh gateway contact and fresh telemetry
	pool.Exec(ctx, `UPDATE gateways SET last_seen_at=now() WHERE id='itest-inv-gw'`)
	pool.Exec(ctx, `UPDATE devices SET config='{"connection":{"interval_seconds":60}}' WHERE id='itest-inv-dev'`)
	pool.Exec(ctx, `UPDATE telemetry SET quality='measured' WHERE tenant_id='itest-inv'`)
	code, o := get("itest-inv", "operator", "north")
	if code != 200 || !strings.Contains(problems(o), "No problem found") {
		t.Fatalf("healthy scope: %d %s", code, problems(o))
	}

	// outage: the gateway went quiet two hours ago, the device stopped with it, and an alert is open
	pool.Exec(ctx, `UPDATE gateways SET last_seen_at=now()-interval '2 hours' WHERE id='itest-inv-gw'`)
	pool.Exec(ctx, `UPDATE telemetry SET observed_at=observed_at-interval '2 hours' WHERE tenant_id='itest-inv'`)
	pool.Exec(ctx, `INSERT INTO alerts(id,tenant_id,severity,message,device_id) VALUES('itest-inv-a1','itest-inv','critical','Boiler temperature stale','itest-inv-dev')`)
	code, o = get("itest-inv", "operator", "North")
	pj := problems(o)
	if code != 200 || !strings.Contains(pj, "is silent") || !strings.Contains(pj, "Boiler temperature stale") || !strings.Contains(pj, `"severity":"critical"`) {
		t.Fatalf("outage: %d %s", code, pj)
	}
	if strings.Contains(pj, "stopped reporting while their gateway is alive") {
		t.Fatalf("a device behind a silent gateway must be blamed on the gateway, once: %s", pj)
	}
	if !strings.Contains(pj, "NEVER_AUTO") || !strings.Contains(pj, "APPROVAL_REQUIRED") || !strings.Contains(pj, "not_examined") {
		t.Fatalf("fix policies and the not-examined list must be present: %s", pj)
	}

	// access and isolation
	if code, _ := get("itest-inv", "viewer", "north"); code != 403 {
		t.Fatalf("viewer: %d", code)
	}
	code, o = get("itest-inv2", "admin", "north")
	if code != 200 || o["report"] != nil || !strings.Contains(o["message"].(string), "No site or asset") {
		t.Fatalf("another tenant must not see this scope: %d %v", code, o)
	}
	if code, _ := get("itest-inv", "admin", ""); code != 400 {
		t.Fatalf("empty scope: %d", code)
	}
	// wildcard characters in the scope are literal, not a way to match everything
	if _, o := get("itest-inv", "admin", "%25"); o["report"] != nil {
		t.Fatalf("a bare percent sign must not match every site: %v", o)
	}
}
