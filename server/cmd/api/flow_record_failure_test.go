package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
)

type flowRecordNotifier struct{ calls chan string }

func (n flowRecordNotifier) Email(_ context.Context, _ []string, _ string, body string) error {
	n.calls <- body
	return nil
}
func (n flowRecordNotifier) Slack(_ context.Context, _ string, body string) error {
	n.calls <- body
	return nil
}

// Delivery is a recording stub only. No mail/webhook/command is sent.
func TestIntegrationFlowRecordFailurePreventsNotification(t *testing.T) {
	s, _ := testServer(t)
	ctx := t.Context()
	const tenant = "itest-flow-record-fault"
	seed(t, s, tenant)
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := s.st.Pool.Exec(c, `DROP TRIGGER IF EXISTS itest_flow_record_fault ON flow_runs; DROP FUNCTION IF EXISTS itest_flow_record_fault(); DELETE FROM flows WHERE tenant_id='itest-flow-record-fault'; DELETE FROM notification_channels WHERE tenant_id='itest-flow-record-fault'`); err != nil {
			t.Errorf("fixture cleanup: %v", err)
		}
	})
	for _, q := range []string{
		`INSERT INTO notification_channels(id,tenant_id,type,target,enabled) VALUES('itest-flow-record-fault-ch','itest-flow-record-fault','email','stub@example.invalid',true)`,
		`INSERT INTO flows(id,tenant_id,name,definition,created_by) VALUES('itest-flow-record-fault-f','itest-flow-record-fault','synthetic only','{}','test-user')`,
		`INSERT INTO flow_versions(id,flow_id,tenant_id,version,definition,status,created_by) VALUES('itest-flow-record-fault-v','itest-flow-record-fault-f','itest-flow-record-fault',1,'{"trigger":{"device_id":"itest-flow-record-fault-dev","point_id":"temp","op":">","value":50},"steps":[{"type":"notify","channel_id":"itest-flow-record-fault-ch","message":"reading {value}"}]}','published','test-user')`,
		`UPDATE flows SET published_version_id='itest-flow-record-fault-v' WHERE id='itest-flow-record-fault-f'`,
		`CREATE FUNCTION itest_flow_record_fault() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.flow_id='itest-flow-record-fault-f' THEN RAISE EXCEPTION 'synthetic flow run refusal'; END IF; RETURN NEW; END$$`,
		`CREATE TRIGGER itest_flow_record_fault BEFORE INSERT ON flow_runs FOR EACH ROW EXECUTE FUNCTION itest_flow_record_fault()`,
	} {
		if _, err := s.st.Pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	n := flowRecordNotifier{calls: make(chan string, 8)}
	flow.Evaluate(ctx, s.st.Pool, n, tenant, tenant+"-dev", "temp", 90)
	var runs int
	if err := s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM flow_runs WHERE flow_id='itest-flow-record-fault-f'`).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("fault did not refuse record: %d %v", runs, err)
	}
	select {
	case body := <-n.calls:
		t.Fatal("notification dispatched despite missing run record", body)
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := s.st.Pool.Exec(ctx, `DROP TRIGGER itest_flow_record_fault ON flow_runs; DROP FUNCTION itest_flow_record_fault()`); err != nil {
		t.Fatal(err)
	}
	flow.Evaluate(ctx, s.st.Pool, n, tenant, tenant+"-dev", "temp", 95)
	select {
	case body := <-n.calls:
		if !strings.Contains(body, "95") {
			t.Fatal("wrong recovered notification", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("healthy recorded path failed to dispatch stub")
	}
	if err := s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM flow_runs WHERE flow_id='itest-flow-record-fault-f' AND trigger_value=95 AND outcome='notified'`).Scan(&runs); err != nil || runs != 1 {
		t.Fatalf("healthy path record: %d %v", runs, err)
	}
	t.Log("failed run INSERT refuses asynchronous notification scheduling; healthy run INSERT reaches recording stub; bounded no-send check, no delivery receipt or general graph-effect/derived-recovery guarantee")
}
