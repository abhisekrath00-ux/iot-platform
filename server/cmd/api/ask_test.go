package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestIntegrationAsk(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-ak")
	seed(t, s, "itest-ak2")
	ctx := t.Context()
	pool := s.st.Pool
	for _, q := range []string{
		`DELETE FROM alerts WHERE tenant_id IN ('itest-ak','itest-ak2')`,
		`DELETE FROM telemetry WHERE tenant_id IN ('itest-ak','itest-ak2')`,
		`UPDATE devices SET name='Boiler One', tags='{boiler}' WHERE id='itest-ak-dev'`,
		`INSERT INTO alerts(id,tenant_id,severity,message,device_id) VALUES('itest-ak-a1','itest-ak','critical','Too hot','itest-ak-dev'),('itest-ak-a2','itest-ak','warning','Vibration','itest-ak-dev'),('itest-ak2-a','itest-ak2','critical','Other tenant','itest-ak2-dev')`,
		`INSERT INTO telemetry(event_id,tenant_id,site_id,gateway_id,device_id,point_id,observed_at,received_at,value,unit,quality,schema_version)
		   VALUES('itest-ak-e1','itest-ak','s','itest-ak-gw','itest-ak-dev','temp',now()-interval '3 hours',now(),55,'C','good',1),
		         ('itest-ak-e2','itest-ak','s','itest-ak-gw','itest-ak-dev','temp',now()-interval '30 minutes',now(),61.5,'C','good',1)`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%q: %v", q, err)
		}
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM telemetry WHERE tenant_id='itest-ak'`)
		pool.Exec(ctx, `DELETE FROM alerts WHERE tenant_id IN ('itest-ak','itest-ak2')`)
	})
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/ask", s.askQuestion)
	ask := func(tenant, q string) (int, map[string]any, string) {
		b, _ := json.Marshal(map[string]string{"question": q})
		w := call(api, tenant, "viewer", "POST", "/v1/ask", string(b))
		var o map[string]any
		json.Unmarshal(w.Body.Bytes(), &o)
		return w.Code, o, w.Body.String()
	}
	code, o, body := ask("itest-ak", "critical alerts")
	rows, _ := o["rows"].([]any)
	if code != 200 || len(rows) != 1 || !strings.Contains(body, "Too hot") || strings.Contains(body, "Other tenant") || !strings.Contains(body, "interpreted_as") {
		t.Fatalf("critical alerts: %d %s", code, body)
	}
	if code, _, body = ask("itest-ak", "alerts on boiler one"); code != 200 || !strings.Contains(body, "Vibration") || !strings.Contains(body, "Too hot") {
		t.Fatalf("alerts on device: %d %s", code, body)
	}
	if code, _, body = ask("itest-ak", "latest temp of Boiler One?"); code != 200 || !strings.Contains(body, "61.5") || strings.Contains(body, `"value":55`) {
		t.Fatalf("latest: %d %s", code, body)
	}
	if code, _, body = ask("itest-ak", "devices tagged boiler"); code != 200 || !strings.Contains(body, "Boiler One") {
		t.Fatalf("tagged: %d %s", code, body)
	}
	if code, _, body = ask("itest-ak2", "latest temp of Boiler One"); code != 404 {
		t.Fatalf("another tenant must not resolve the device: %d %s", code, body)
	}
	if code, _, body = ask("itest-ak", "alerts on nobody"); code != 404 {
		t.Fatalf("unknown device: %d %s", code, body)
	}
	// not understood: refused with examples, nothing is run
	for _, q := range []string{"turn off the siren", "write 5 to Boiler One", "'; drop table alerts; --", ""} {
		if code, _, body = ask("itest-ak", q); code != 422 || !strings.Contains(body, "examples") {
			t.Fatalf("%q: %d %s", q, code, body)
		}
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE tenant_id='itest-ak'`).Scan(&n)
	if n != 2 {
		t.Fatalf("a question must never change data: %d alerts", n)
	}
}
