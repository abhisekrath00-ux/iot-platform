package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type flakyMail struct {
	mu   sync.Mutex
	fail bool
	n    int
}

func (f *flakyMail) Email(_ context.Context, _ []string, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	if f.fail {
		return errors.New("smtp: connection refused")
	}
	return nil
}
func (f *flakyMail) Slack(context.Context, string, string) error { return nil }

func TestIntegrationChannelAdmin(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-ca1")
	seed(t, s, "itest-ca2")
	ctx := t.Context()
	pool := s.st.Pool
	clean := func() {
		pool.Exec(ctx, `DELETE FROM escalation_steps WHERE tenant_id IN ('itest-ca1','itest-ca2')`)
		pool.Exec(ctx, `DELETE FROM notification_channels WHERE tenant_id IN ('itest-ca1','itest-ca2')`)
		pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id IN ('itest-ca1','itest-ca2')`)
		pool.Exec(ctx, `DELETE FROM users WHERE id IN ('ca-admin','ca-op','ca-admin2')`)
	}
	clean()
	t.Cleanup(clean)
	for _, u := range [][3]string{{"ca-admin", "itest-ca1", "admin"}, {"ca-op", "itest-ca1", "operator"}, {"ca-admin2", "itest-ca2", "admin"}} {
		pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES($1,$2,$1||'@ca-test.example','U',$3)`, u[0], u[1], u[2])
	}
	fm := &flakyMail{}
	s.notifier = fm
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/notifications/channels/{id}/test", s.testChannel)
	mux.HandleFunc("PATCH /v1/notifications/channels/{id}", s.patchChannel)
	mux.HandleFunc("DELETE /v1/notifications/channels/{id}", s.deleteChannel)
	h := s.activeUser(mux)
	as := func(tenant, user, role, method, path, body string) (int, string) {
		w := callAs(h, tenant, user, role, method, path, body)
		return w.Code, w.Body.String()
	}
	pool.Exec(ctx, `INSERT INTO notification_channels(id,tenant_id,type,target) VALUES('ca-ch1','itest-ca1','email','ops@plant.example'),('ca-ch2','itest-ca1','email','x@plant.example')`)

	// a working channel reports ok and really sends; a failing one reports the error
	if c, b := as("itest-ca1", "ca-admin", "admin", "POST", "/v1/notifications/channels/ca-ch1/test", ""); c != 200 || !strings.Contains(b, `"ok":true`) || fm.n != 1 {
		t.Fatalf("test ok = %d %s sends=%d", c, b, fm.n)
	}
	fm.fail = true
	channelTestAt.Delete("ca-ch1")
	if c, b := as("itest-ca1", "ca-admin", "admin", "POST", "/v1/notifications/channels/ca-ch1/test", ""); c != 200 || !strings.Contains(b, `"ok":false`) || !strings.Contains(b, "connection refused") {
		t.Fatalf("test fail = %d %s", c, b)
	}
	// throttled, admin only, tenant scoped
	if c, _ := as("itest-ca1", "ca-admin", "admin", "POST", "/v1/notifications/channels/ca-ch1/test", ""); c != 429 {
		t.Fatalf("second test within 10 s = %d, want 429", c)
	}
	if c, _ := as("itest-ca1", "ca-op", "operator", "POST", "/v1/notifications/channels/ca-ch2/test", ""); c != 403 {
		t.Fatalf("operator test = %d", c)
	}
	if c, _ := as("itest-ca2", "ca-admin2", "admin", "POST", "/v1/notifications/channels/ca-ch2/test", ""); c != 404 {
		t.Fatalf("cross-tenant test = %d", c)
	}
	sends := fm.n
	if c, _ := as("itest-ca2", "ca-admin2", "admin", "PATCH", "/v1/notifications/channels/ca-ch2", `{"enabled":false}`); c != 404 {
		t.Fatalf("cross-tenant patch = %d", c)
	}
	if c, _ := as("itest-ca2", "ca-admin2", "admin", "DELETE", "/v1/notifications/channels/ca-ch2", ""); c != 404 {
		t.Fatalf("cross-tenant delete = %d", c)
	}
	var left int
	pool.QueryRow(ctx, `SELECT count(*) FROM notification_channels WHERE id='ca-ch2' AND enabled`).Scan(&left)
	if left != 1 || fm.n != sends {
		t.Fatal("another tenant changed or used the channel")
	}
	// disable, then the channel is off
	if c, _ := as("itest-ca1", "ca-admin", "admin", "PATCH", "/v1/notifications/channels/ca-ch2", `{"enabled":false}`); c != 204 {
		t.Fatalf("disable = %d", c)
	}
	if c, _ := as("itest-ca1", "ca-admin", "admin", "PATCH", "/v1/notifications/channels/ca-ch2", `{}`); c != 400 {
		t.Fatalf("empty patch = %d", c)
	}
	// delete is refused while an escalation step uses the channel (it would be dropped silently), then allowed
	pool.Exec(ctx, `INSERT INTO escalation_steps(id,tenant_id,severity,step,after_minutes,channel_id) VALUES('ca-es1','itest-ca1','critical',1,5,'ca-ch1')`)
	if c, b := as("itest-ca1", "ca-admin", "admin", "DELETE", "/v1/notifications/channels/ca-ch1", ""); c != 409 || !strings.Contains(b, "escalation steps") {
		t.Fatalf("delete in use = %d %s", c, b)
	}
	pool.Exec(ctx, `DELETE FROM escalation_steps WHERE id='ca-es1'`)
	if c, _ := as("itest-ca1", "ca-admin", "admin", "DELETE", "/v1/notifications/channels/ca-ch1", ""); c != 204 {
		t.Fatalf("delete = %d", c)
	}
	var audits int
	pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id='itest-ca1' AND action LIKE 'channel.%'`).Scan(&audits)
	if audits != 4 { // 2 tests, 1 update, 1 delete
		t.Fatalf("audit rows = %d, want 4", audits)
	}
}
