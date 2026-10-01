package main

import (
	"context"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
)

type nopNotifier struct{}

func (nopNotifier) Email(context.Context, []string, string, string) error { return nil }
func (nopNotifier) Slack(context.Context, string, string) error           { return nil }

func TestIntegrationFlowCooldownSuppressesRepeats(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-fc1")
	ctx := t.Context()
	pool := s.st.Pool
	pool.Exec(ctx, `DELETE FROM flows WHERE tenant_id='itest-fc1'`)
	def := `{"trigger":{"device_id":"itest-fc1-dev","point_id":"temp","op":">","value":50},"steps":[{"type":"notify","channel_id":"itest-fc1-ch"}],"cooldown_seconds":3600}`
	for _, q := range []string{
		`INSERT INTO notification_channels(id,tenant_id,type,target) VALUES('itest-fc1-ch','itest-fc1','email','ops@example.com') ON CONFLICT DO NOTHING`,
		`INSERT INTO flows(id,tenant_id,name,definition,created_by) VALUES('itest-fc1-f','itest-fc1','Hot','` + def + `','t')`,
		`INSERT INTO flow_versions(id,flow_id,tenant_id,version,definition,status,created_by) VALUES('itest-fc1-v','itest-fc1-f','itest-fc1',1,'` + def + `','published','t')`,
		`UPDATE flows SET published_version_id='itest-fc1-v' WHERE id='itest-fc1-f'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for i := 0; i < 3; i++ {
		flow.Evaluate(ctx, pool, nopNotifier{}, "itest-fc1", "itest-fc1-dev", "temp", 90)
	}
	count := func(outcome string) int {
		var n int
		pool.QueryRow(ctx, `SELECT count(*) FROM flow_runs WHERE flow_id='itest-fc1-f' AND outcome=$1`, outcome).Scan(&n)
		return n
	}
	if n, sup := count("notified"), count("suppressed_cooldown"); n != 1 || sup != 2 {
		t.Fatalf("notified=%d suppressed=%d, want 1 and 2", n, sup)
	}
}
