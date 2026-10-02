package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/rules"
)

func TestIntegrationSigmaAndKPIBandRules(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-an1")
	seed(t, s, "itest-an2")
	ctx := context.Background()
	pool := s.st.Pool
	clean := func() {
		for _, q := range []string{
			`DELETE FROM alerts WHERE tenant_id IN ('itest-an1','itest-an2')`,
			`DELETE FROM rules WHERE tenant_id IN ('itest-an1','itest-an2')`,
			`DELETE FROM kpis WHERE tenant_id IN ('itest-an1','itest-an2')`,
		} {
			pool.Exec(ctx, q)
		}
	}
	clean()
	t.Cleanup(clean)
	for _, q := range []string{
		`INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('an-user','itest-an1','an@x.local','an','admin') ON CONFLICT DO NOTHING`,
		// 60 measured samples of 20..22, one per minute, ending 2 minutes ago
		`DELETE FROM telemetry WHERE tenant_id='itest-an1' AND point_id='vib'`,
		`INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,value,unit,schema_version)
		 SELECT 'an-'||g, 'itest-an1','itest-an1-gw','itest-an1-dev','vib', now() - make_interval(mins => g+2), 20 + (g % 3), 'mm/s', 1 FROM generate_series(1,60) g`,
		`INSERT INTO kpis(id,tenant_id,name,expression,unit,created_by) VALUES('an-kpi','itest-an1','Vib x2','{itest-an1-dev.vib} * 2','mm/s','an-user')`,
		`INSERT INTO rules(id,tenant_id,name,definition,enabled,created_by) VALUES
		 ('an-sigma','itest-an1','vib sigma','{"kind":"sigma","point_id":"vib","sigma":4,"window_minutes":120,"severity":"warning"}',true,'an-user'),
		 ('an-off','itest-an1','disabled sigma','{"kind":"sigma","point_id":"vib","sigma":2,"window_minutes":120,"severity":"info"}',false,'an-user'),
		 ('an-band','itest-an1','vib band','{"kind":"kpi_band","kpi_id":"an-kpi","max":50,"severity":"critical"}',true,'an-user')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	count := func(rule string) (n int) {
		pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE rule_id=$1`, rule).Scan(&n)
		return
	}
	rules.Evaluate(ctx, pool, nil, "itest-an1", "itest-an1-dev", "vib", 21) // typical
	if count("an-sigma") != 0 {
		t.Fatal("typical reading raised a sigma alert")
	}
	rules.Evaluate(ctx, pool, nil, "itest-an1", "itest-an1-dev", "vib", 60) // far outside
	if count("an-sigma") != 1 {
		t.Fatalf("outlier alerts = %d, want 1", count("an-sigma"))
	}
	rules.Evaluate(ctx, pool, nil, "itest-an1", "itest-an1-dev", "vib", 61) // deduped while open
	if count("an-sigma") != 1 || count("an-off") != 0 {
		t.Fatal("dedupe or disabled rule broken")
	}
	// another tenant's reading never uses this tenant's history or rules
	rules.Evaluate(ctx, pool, nil, "itest-an2", "itest-an2-dev", "vib", 60)
	if count("an-sigma") != 1 {
		t.Fatal("cross-tenant evaluation")
	}
	// KPI band: latest vib is about 20-22 so the KPI is ~42, inside max 50; no alert
	rules.EvaluateKPIs(ctx, pool, nil)
	if count("an-band") != 0 {
		t.Fatalf("in-band KPI alerted")
	}
	pool.Exec(ctx, `INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,value,unit,schema_version)
		VALUES('an-hot','itest-an1','itest-an1-gw','itest-an1-dev','vib', now(), 40,'mm/s',1)`)
	rules.EvaluateKPIs(ctx, pool, nil)
	rules.EvaluateKPIs(ctx, pool, nil) // deduped
	if count("an-band") != 1 {
		t.Fatalf("band alerts = %d, want 1", count("an-band"))
	}

	// creation through the API validates kind, tenant ownership of the KPI, and role
	pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('test-user','itest-an1','tu@x.local','tu','admin') ON CONFLICT DO NOTHING`)
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/rules", s.createRule)
	post := func(tenant, role, def string) int {
		b, _ := json.Marshal(map[string]any{"name": "r", "definition": json.RawMessage(def), "enabled": false})
		return call(api, tenant, role, "POST", "/v1/rules", string(b)).Code
	}
	if c := post("itest-an1", "operator", `{"kind":"sigma","point_id":"vib","sigma":3,"window_minutes":60,"severity":"info"}`); c != 201 {
		t.Fatalf("valid sigma %d", c)
	}
	if c := post("itest-an1", "operator", `{"kind":"sigma","point_id":"vib","sigma":0.5,"window_minutes":60,"severity":"info"}`); c != 400 {
		t.Fatalf("bad sigma %d", c)
	}
	if c := post("itest-an2", "operator", `{"kind":"kpi_band","kpi_id":"an-kpi","max":1,"severity":"info"}`); c != 404 {
		t.Fatalf("other tenant's kpi %d", c)
	}
	if c := post("itest-an1", "viewer", `{"kind":"sigma","point_id":"vib","sigma":3,"window_minutes":60,"severity":"info"}`); c != 403 {
		t.Fatalf("viewer %d", c)
	}
}

func TestIntegrationForecastLimitRule(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-fl")
	ctx := t.Context()
	pool := s.st.Pool
	pool.Exec(ctx, `DELETE FROM telemetry_rollup_hourly WHERE tenant_id='itest-fl'`)
	pool.Exec(ctx, `DELETE FROM alerts WHERE tenant_id='itest-fl'`)
	pool.Exec(ctx, `DELETE FROM rules WHERE tenant_id='itest-fl'`)
	end := time.Now().UTC().Truncate(time.Hour)
	for i := 1; i <= 24*14; i++ {
		b := end.Add(-time.Duration(i) * time.Hour)
		v := 50 + 10*math.Sin(2*math.Pi*float64(b.Hour())/24) + float64((i*7919)%11)/20 + 0.05*float64(24*14-i)
		pool.Exec(ctx, `INSERT INTO telemetry_rollup_hourly(tenant_id,device_id,point_id,bucket,n,sum,min,max) VALUES('itest-fl','d1','temp',$1,1,$2,$2,$2)`, b, v)
	}
	mk := func(id string, limit float64) {
		pool.Exec(ctx, `INSERT INTO rules(id,tenant_id,name,definition,enabled,created_by) VALUES($1,'itest-fl',$1,$2::jsonb,true,'test-user')`, id,
			fmt.Sprintf(`{"kind":"forecast_limit","device_id":"d1","point_id":"temp","op":">","threshold":%v,"horizon_hours":24,"severity":"warning"}`, limit))
	}
	mk("fl-far", 1000)
	mk("fl-near", 60)
	rules.EvaluateForecasts(ctx, pool, nil)
	rules.EvaluateForecasts(ctx, pool, nil) // deduped
	count := func(rule string) int {
		var n int
		pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE rule_id=$1`, rule).Scan(&n)
		return n
	}
	if count("fl-far") != 0 {
		t.Fatal("unreachable limit alerted")
	}
	if count("fl-near") != 1 {
		t.Fatalf("near-limit alerts = %d, want 1", count("fl-near"))
	}
	// the per-reading path must ignore this kind entirely
	rules.Evaluate(ctx, pool, nil, "itest-fl", "d1", "temp", 9999)
	if count("fl-far") != 0 {
		t.Fatal("forecast rule fired on a raw reading")
	}
}
