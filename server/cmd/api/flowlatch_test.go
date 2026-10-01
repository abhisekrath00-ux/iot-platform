package main

import (
	"context"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
)

func TestIntegrationFlowLatchRearms(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-fl1")
	ctx := t.Context()
	pool := s.st.Pool
	pool.Exec(ctx, `DELETE FROM flows WHERE tenant_id='itest-fl1'`)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM flows WHERE tenant_id='itest-fl1'`) })
	def := `{"trigger":{"device_id":"itest-fl1-dev","point_id":"temp","op":">","value":50},"steps":[{"type":"notify","channel_id":"itest-fl1-ch"}],"latch":true}`
	for _, q := range []string{
		`INSERT INTO notification_channels(id,tenant_id,type,target) VALUES('itest-fl1-ch','itest-fl1','email','ops@example.com') ON CONFLICT DO NOTHING`,
		`INSERT INTO flows(id,tenant_id,name,definition,created_by) VALUES('itest-fl1-f','itest-fl1','Hot','` + def + `','t')`,
		`INSERT INTO flow_versions(id,flow_id,tenant_id,version,definition,status,created_by) VALUES('fv-itest-fl1-f','itest-fl1-f','itest-fl1',1,'` + def + `','published','t')`,
		`UPDATE flows SET published_version_id='fv-itest-fl1-f' WHERE id='itest-fl1-f'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// hot, hot, back to normal, hot again: two notifications, one latch suppression
	for _, v := range []float64{90, 95, 10, 90} {
		flow.Evaluate(ctx, pool, nopNotifier{}, "itest-fl1", "itest-fl1-dev", "temp", v)
	}
	count := func(outcome string) int {
		var n int
		pool.QueryRow(ctx, `SELECT count(*) FROM flow_runs WHERE flow_id='itest-fl1-f' AND outcome=$1`, outcome).Scan(&n)
		return n
	}
	if n, sup := count("notified"), count("suppressed_latch"); n != 2 || sup != 1 {
		t.Fatalf("notified=%d suppressed_latch=%d, want 2 and 1", n, sup)
	}
}
