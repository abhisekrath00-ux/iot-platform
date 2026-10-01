package main

import (
	"context"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
)

func TestIntegrationScheduledInjectFiresOncePerInterval(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-fi1")
	ctx := t.Context()
	pool := s.st.Pool
	pool.Exec(ctx, `DELETE FROM flows WHERE tenant_id='itest-fi1'`)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM flows WHERE tenant_id='itest-fi1'`) })
	def := `{"graph":{"nodes":[{"id":"i","type":"inject","seconds":3600,"value":7,"device_id":"d","point_id":"p"},{"id":"n","type":"notify","channel_id":"itest-fi1-ch","message":"tick {value}"}],"edges":[{"from":"i","to":"n"}]}}`
	for _, q := range []string{
		`INSERT INTO notification_channels(id,tenant_id,type,target) VALUES('itest-fi1-ch','itest-fi1','email','ops@example.com') ON CONFLICT DO NOTHING`,
		`INSERT INTO flows(id,tenant_id,name,definition,created_by) VALUES('itest-fi1-f','itest-fi1','Tick','` + def + `','t')`,
		`INSERT INTO flow_versions(id,flow_id,tenant_id,version,definition,status,created_by) VALUES('fv-itest-fi1-f','itest-fi1-f','itest-fi1',1,'` + def + `','published','t')`,
		`UPDATE flows SET published_version_id='fv-itest-fi1-f' WHERE id='itest-fi1-f'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	runs := func() int {
		var n int
		pool.QueryRow(ctx, `SELECT count(*) FROM flow_runs WHERE flow_id='itest-fi1-f'`).Scan(&n)
		return n
	}
	flow.RunScheduled(ctx, pool, nopNotifier{})
	flow.RunScheduled(ctx, pool, nopNotifier{}) // inside the interval: must not fire again
	if n := runs(); n != 1 {
		t.Fatalf("runs after two ticks = %d, want 1", n)
	}
	// a reading must never start an inject flow
	flow.Evaluate(ctx, pool, nopNotifier{}, "itest-fi1", "d", "p", 99)
	if n := runs(); n != 1 {
		t.Fatalf("a reading started an inject flow: %d runs", n)
	}
	// once the interval has passed it fires again
	pool.Exec(ctx, `UPDATE flow_runs SET created_at = now() - interval '2 hours' WHERE flow_id='itest-fi1-f'`)
	flow.RunScheduled(ctx, pool, nopNotifier{})
	if n := runs(); n != 2 {
		t.Fatalf("runs after interval = %d, want 2", n)
	}
	// a disabled flow does not run
	pool.Exec(ctx, `UPDATE flow_runs SET created_at = now() - interval '2 hours' WHERE flow_id='itest-fi1-f'`)
	pool.Exec(ctx, `UPDATE flows SET enabled=false WHERE id='itest-fi1-f'`)
	flow.RunScheduled(ctx, pool, nopNotifier{})
	if n := runs(); n != 2 {
		t.Fatalf("disabled flow ran: %d", n)
	}
}
