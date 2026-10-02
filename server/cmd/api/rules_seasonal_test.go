package main

import (
	"context"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/rules"
)

func TestIntegrationSeasonalRule(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-sea")
	ctx := context.Background()
	pool := s.st.Pool
	clean := func() {
		pool.Exec(ctx, `DELETE FROM alerts WHERE tenant_id='itest-sea'`)
		pool.Exec(ctx, `DELETE FROM rules WHERE tenant_id='itest-sea'`)
		pool.Exec(ctx, `DELETE FROM telemetry WHERE tenant_id='itest-sea'`)
		pool.Exec(ctx, `DELETE FROM telemetry_rollup_hourly WHERE tenant_id='itest-sea'`)
		pool.Exec(ctx, `DELETE FROM telemetry_rollup_daily WHERE tenant_id='itest-sea'`)
	}
	clean()
	t.Cleanup(clean)
	for _, q := range []string{
		`INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('sea-user','itest-sea','sea@x.local','sea','admin') ON CONFLICT DO NOTHING`,
		// 10 days x 4 samples at THIS hour of day (UTC): 20..22
		`INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,value,unit,schema_version)
		 SELECT 'sea-'||d||'-'||m, 'itest-sea','itest-sea-gw','itest-sea-dev','load', date_trunc('hour', now()) - make_interval(days => d) + make_interval(mins => m), 20 + (m % 3), 'kW', 1
		 FROM generate_series(1,10) d, generate_series(1,4) m`,
		// decoy: a high daily peak 12 hours away must not count as this hour's baseline
		`INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,value,unit,schema_version)
		 SELECT 'seap-'||d||'-'||m, 'itest-sea','itest-sea-gw','itest-sea-dev','load', date_trunc('hour', now()) - make_interval(days => d, hours => 12) + make_interval(mins => m), 90, 'kW', 1
		 FROM generate_series(1,10) d, generate_series(1,4) m`,
		`INSERT INTO rules(id,tenant_id,name,definition,enabled,created_by) VALUES
		 ('sea-r','itest-sea','seasonal','{"kind":"seasonal","point_id":"load","sigma":4,"window_days":14,"severity":"warning"}',true,'sea-user')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	count := func() (n int) {
		pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE rule_id='sea-r'`).Scan(&n)
		return
	}
	rules.Evaluate(ctx, pool, nil, "itest-sea", "itest-sea-dev", "load", 21)
	if count() != 0 {
		t.Fatal("usual value for this hour alerted")
	}
	// 90 is normal at the decoy hour but far outside this hour's 20..22
	rules.Evaluate(ctx, pool, nil, "itest-sea", "itest-sea-dev", "load", 90)
	if count() != 1 {
		t.Fatalf("alerts = %d, want 1", count())
	}
}
