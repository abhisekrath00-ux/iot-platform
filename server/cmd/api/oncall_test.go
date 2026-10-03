package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/rules"
)

func TestIntegrationOnCallEscalation(t *testing.T) {
	s, h := testServer(t)
	seed(t, s, "itest-oc")
	seed(t, s, "itest-oc2")
	ctx := context.Background()
	pool := s.st.Pool
	for _, q := range []string{
		`DELETE FROM escalation_steps WHERE tenant_id IN ('itest-oc','itest-oc2')`,
		`DELETE FROM oncall_schedules WHERE tenant_id IN ('itest-oc','itest-oc2')`,
		`DELETE FROM alerts WHERE tenant_id='itest-oc'`,
		`DELETE FROM notification_channels WHERE tenant_id IN ('itest-oc','itest-oc2')`,
		`INSERT INTO notification_channels(id,tenant_id,type,target) VALUES('itest-oc-a','itest-oc','email','alice@example.com'),('itest-oc-b','itest-oc','email','bob@example.com')`,
		`INSERT INTO notification_channels(id,tenant_id,type,target) VALUES('itest-oc2-x','itest-oc2','email','x@example.com')`,
		`INSERT INTO alerts(id,tenant_id,severity,message,created_at) VALUES('itest-oc-al','itest-oc','warning','Pump trip', now() - interval '20 minutes')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%q: %v", q, err)
		}
	}
	// the shift that started 1 hour ago belongs to the first channel (24 h shifts), so alice is on call
	body := func(chs string) string {
		return fmt.Sprintf(`{"name":"Plant","anchor":%q,"shift_hours":24,"channel_ids":[%s]}`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), chs)
	}
	if w := call(h, "itest-oc", "viewer", "POST", "/v1/oncall", body(`"itest-oc-a","itest-oc-b"`)); w.Code != 403 {
		t.Fatalf("viewer: %d", w.Code)
	}
	if w := call(h, "itest-oc", "admin", "POST", "/v1/oncall", body(`"itest-oc-a","itest-oc2-x"`)); w.Code != 400 {
		t.Fatalf("another tenant's channel: %d %s", w.Code, w.Body.String())
	}
	w := call(h, "itest-oc", "admin", "POST", "/v1/oncall", body(`"itest-oc-a","itest-oc-b"`))
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if w := call(h, "itest-oc", "admin", "POST", "/v1/oncall", body(`"itest-oc-a","itest-oc-b"`)); w.Code != 409 {
		t.Fatalf("duplicate name: %d", w.Code)
	}
	var sid string
	pool.QueryRow(ctx, `SELECT id FROM oncall_schedules WHERE tenant_id='itest-oc'`).Scan(&sid)
	if w := call(h, "itest-oc", "viewer", "GET", "/v1/oncall", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"on_call_channel":"itest-oc-a"`) {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	if w := call(h, "itest-oc2", "admin", "PUT", "/v1/escalation", `{"steps":[{"step":1,"after_minutes":10,"schedule_id":"`+sid+`"}]}`); w.Code != 400 {
		t.Fatalf("another tenant's schedule: %d", w.Code)
	}
	if w := call(h, "itest-oc", "admin", "PUT", "/v1/escalation", `{"steps":[{"step":1,"after_minutes":10,"schedule_id":"`+sid+`"}]}`); w.Code != 200 {
		t.Fatalf("policy with schedule: %d %s", w.Code, w.Body.String())
	}
	if w := call(h, "itest-oc", "admin", "DELETE", "/v1/oncall/"+sid, ""); w.Code != 409 {
		t.Fatalf("delete of a schedule in use: %d", w.Code)
	}
	fn := &ocNotifier{}
	rules.EvaluateEscalations(ctx, pool, fn)
	if len(fn.emails) != 1 || !strings.Contains(fn.emails[0], "alice@example.com") || strings.Contains(fn.emails[0], "bob@") {
		t.Fatalf("on-call alice must get it: %v", fn.emails)
	}
	// the rotation moved on: next alert goes to bob
	pool.Exec(ctx, `UPDATE oncall_schedules SET anchor = now() - interval '25 hours' WHERE id=$1`, sid)
	pool.Exec(ctx, `INSERT INTO alerts(id,tenant_id,severity,message,created_at) VALUES('itest-oc-al2','itest-oc','warning','Fan stuck', now() - interval '20 minutes')`)
	rules.EvaluateEscalations(ctx, pool, fn)
	if len(fn.emails) != 2 || !strings.Contains(fn.emails[1], "bob@example.com") {
		t.Fatalf("on-call bob must get the second: %v", fn.emails)
	}
	call(h, "itest-oc", "admin", "PUT", "/v1/escalation", `{"steps":[]}`)
	if w := call(h, "itest-oc", "admin", "DELETE", "/v1/oncall/"+sid, ""); w.Code != 204 {
		t.Fatalf("delete: %d", w.Code)
	}
}

// ocNotifier records only this test's recipients (the sweeper is global in a shared database).
type ocNotifier struct{ emails []string }

func (f *ocNotifier) Email(_ context.Context, to []string, _, body string) error {
	if to[0] == "alice@example.com" || to[0] == "bob@example.com" {
		f.emails = append(f.emails, to[0]+"|"+body)
	}
	return nil
}
func (f *ocNotifier) Slack(context.Context, string, string) error { return nil }
