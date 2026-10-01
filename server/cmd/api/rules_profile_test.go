package main

import (
	"context"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/rules"
)

func TestIntegrationProfileRuleCoversManyDevicesOneAlertEach(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-pr1")
	ctx := context.Background()
	pool := s.st.Pool
	for _, q := range []string{
		`INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('pr-user','itest-pr1','pr@x.local','pr','admin') ON CONFLICT DO NOTHING`,
		`DELETE FROM alerts WHERE tenant_id='itest-pr1'`,
		`DELETE FROM rules WHERE tenant_id='itest-pr1'`,
		`INSERT INTO devices(id,tenant_id,gateway_id,profile,name) VALUES('itest-pr1-b','itest-pr1','itest-pr1-gw','modbus-tcp','Second PLC'),('itest-pr1-c','itest-pr1','itest-pr1-gw','door-contact','Door')`,
		`INSERT INTO rules(id,tenant_id,name,definition,enabled,created_by) VALUES('pr-rule','itest-pr1','hot plc','{"point_id":"temp","op":">","threshold":90,"severity":"warning","profile":"modbus-tcp"}',true,'pr-user')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM alerts WHERE tenant_id='itest-pr1'`)
		pool.Exec(ctx, `DELETE FROM rules WHERE tenant_id='itest-pr1'`)
	})
	count := func() (n int) {
		pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE tenant_id='itest-pr1' AND rule_id='pr-rule'`).Scan(&n)
		return
	}
	rules.Evaluate(ctx, pool, nil, "itest-pr1", "itest-pr1-dev", "temp", 95) // matching profile
	rules.Evaluate(ctx, pool, nil, "itest-pr1", "itest-pr1-b", "temp", 99)   // same profile, second device
	rules.Evaluate(ctx, pool, nil, "itest-pr1", "itest-pr1-c", "temp", 99)   // other profile: ignored
	rules.Evaluate(ctx, pool, nil, "itest-pr1", "itest-pr1-b", "temp", 99)   // repeat: deduped per device
	if n := count(); n != 2 {
		t.Fatalf("alerts = %d, want 2 (one per matching device)", n)
	}
	rules.Evaluate(ctx, pool, nil, "itest-pr1", "itest-pr1-dev", "temp", 50) // below threshold
	if n := count(); n != 2 {
		t.Fatalf("below threshold raised an alert: %d", n)
	}
}
