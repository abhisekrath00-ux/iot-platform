package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// keyRequest builds a request with the auth context and an optional Idempotency-Key header.
func keyRequest(tenant, user, role, path, key, body string) (*httptest.ResponseRecorder, *http.Request) {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	ctx := context.WithValue(r.Context(), auth.CtxTenant, tenant)
	ctx = context.WithValue(ctx, auth.CtxUser, user)
	ctx = context.WithValue(ctx, auth.CtxRole, role)
	return httptest.NewRecorder(), r.WithContext(ctx)
}

// Idempotency-Key on the wizard-driven POSTs: a retried request replays the first response instead of
// writing twice; a reused key with a different body is a 409; a key still in flight is a 409; replay
// happens only after the handler's own role check, so a demoted user cannot replay a stored success.
func TestIntegrationIdempotency(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-id1")
	seed(t, s, "itest-id2")
	ctx := context.Background()
	pool := s.st.Pool
	clean := func() {
		pool.Exec(ctx, `DELETE FROM idempotency_keys WHERE tenant_id IN ('itest-id1','itest-id2')`)
		pool.Exec(ctx, `DELETE FROM devices WHERE tenant_id IN ('itest-id1','itest-id2') AND name LIKE 'Idem %'`)
		pool.Exec(ctx, `DELETE FROM notification_channels WHERE tenant_id IN ('itest-id1','itest-id2') AND target LIKE '%@idem.example'`)
		pool.Exec(ctx, `DELETE FROM user_invites WHERE tenant_id IN ('itest-id1','itest-id2')`)
		pool.Exec(ctx, `DELETE FROM users WHERE id IN ('idem-admin','idem-op','idem-viewer','idem-op2','idem-scoped')`)
		pool.Exec(ctx, `DELETE FROM user_customer_scope WHERE tenant_id='itest-id1'`)
		pool.Exec(ctx, `DELETE FROM customers WHERE id='idem-cust'`)
	}
	clean()
	t.Cleanup(clean)
	pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES
		('idem-admin','itest-id1','idem-admin@idem.example','A','admin'),
		('idem-op','itest-id1','idem-op@idem.example','O','operator'),
		('idem-viewer','itest-id1','idem-viewer@idem.example','V','viewer'),
		('idem-scoped','itest-id1','idem-scoped@idem.example','S','operator'),
		('idem-op2','itest-id2','idem-op2@idem.example','O2','operator')`)
	pool.Exec(ctx, `INSERT INTO customers(id,tenant_id,name) VALUES('idem-cust','itest-id1','C')`)
	pool.Exec(ctx, `INSERT INTO user_customer_scope(tenant_id,user_id,customer_id) VALUES('itest-id1','idem-scoped','idem-cust')`)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/devices", s.createDevice)
	mux.HandleFunc("POST /v1/notifications/channels", s.createChannel)
	mux.HandleFunc("POST /v1/users/invites", s.createInvite)
	h := s.activeUser(s.customerScope(mux))
	asKey := func(tenant, user, role, path, key, body string) (int, string) {
		w, r := keyRequest(tenant, user, role, path, key, body)
		h.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	devBody := `{"gateway_id":"itest-id1-gw","profile":"modbus-tcp","name":"Idem Pump","config":{}}`

	// 1. uncertain-response retry: same key + same body replays the first response; one device exists.
	c1, b1 := asKey("itest-id1", "idem-op", "operator", "/v1/devices", "k-dev-1", devBody)
	c2, b2 := asKey("itest-id1", "idem-op", "operator", "/v1/devices", "k-dev-1", devBody)
	if c1 != 201 || c2 != 201 || b1 != b2 {
		t.Fatalf("create/replay = %d %s / %d %s", c1, b1, c2, b2)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM devices WHERE tenant_id='itest-id1' AND name='Idem Pump'`).Scan(&n)
	if n != 1 {
		t.Fatalf("devices after replay = %d, want 1", n)
	}

	// 2. same key, changed body: 409, still one device.
	c3, _ := asKey("itest-id1", "idem-op", "operator", "/v1/devices", "k-dev-1", strings.Replace(devBody, "Idem Pump", "Idem Other", 1))
	if c3 != 409 {
		t.Fatalf("changed payload = %d, want 409", c3)
	}
	pool.QueryRow(ctx, `SELECT count(*) FROM devices WHERE tenant_id='itest-id1' AND name LIKE 'Idem%'`).Scan(&n)
	if n != 1 {
		t.Fatalf("devices after changed-payload 409 = %d, want 1", n)
	}

	// 3. a key still in flight: 409.
	pool.Exec(ctx, `INSERT INTO idempotency_keys(tenant_id,user_id,action,key,payload_hash) VALUES('itest-id1','idem-op','device.create','k-dev-fly','x')`)
	c4, _ := asKey("itest-id1", "idem-op", "operator", "/v1/devices", "k-dev-fly", devBody)
	if c4 != 409 {
		t.Fatalf("in-flight = %d, want 409", c4)
	}

	// 4. no key: two POSTs create two devices (documents the pre-existing behaviour keys protect against).
	asKey("itest-id1", "idem-op", "operator", "/v1/devices", "", strings.Replace(devBody, "Idem Pump", "Idem NoKey", 1))
	asKey("itest-id1", "idem-op", "operator", "/v1/devices", "", strings.Replace(devBody, "Idem Pump", "Idem NoKey", 1))
	pool.QueryRow(ctx, `SELECT count(*) FROM devices WHERE tenant_id='itest-id1' AND name='Idem NoKey'`).Scan(&n)
	if n != 2 {
		t.Fatalf("no-key double POST = %d devices, want 2 (unchanged legacy behaviour)", n)
	}

	// 5. replay re-checks current authorization: demote the operator, the stored success must not replay.
	pool.Exec(ctx, `UPDATE users SET role='viewer' WHERE id='idem-op'`)
	c5, _ := asKey("itest-id1", "idem-op", "viewer", "/v1/devices", "k-dev-1", devBody)
	if c5 != 403 {
		t.Fatalf("replay after demotion = %d, want 403", c5)
	}
	pool.Exec(ctx, `UPDATE users SET role='operator' WHERE id='idem-op'`)

	// 6. key scope: the same key string is independent for another action, user and tenant.
	c6, _ := asKey("itest-id1", "idem-admin", "admin", "/v1/notifications/channels", "k-dev-1", `{"type":"email","target":"ops@idem.example"}`)
	if c6 != 201 {
		t.Fatalf("same key, different action = %d, want 201", c6)
	}
	c7, _ := asKey("itest-id2", "idem-op2", "operator", "/v1/devices", "k-dev-1", `{"gateway_id":"itest-id2-gw","profile":"modbus-tcp","name":"Idem Pump","config":{}}`)
	if c7 != 201 {
		t.Fatalf("same key, other tenant = %d, want 201", c7)
	}

	// 7. invite retry: one invite row, one email, identical replayed body.
	t.Setenv("LOCAL_LOGIN", "1")
	t.Setenv("TEST_MAIL", "1")
	fm := &flakyMail{}
	s.notifier = fm
	invBody := `{"email":"newjoin@idem.example","role":"viewer","send_email":true}`
	i1, ib1 := asKey("itest-id1", "idem-admin", "admin", "/v1/users/invites", "k-inv-1", invBody)
	i2, ib2 := asKey("itest-id1", "idem-admin", "admin", "/v1/users/invites", "k-inv-1", invBody)
	if i1 != 201 || i2 != 201 || ib1 != ib2 {
		t.Fatalf("invite create/replay = %d %s / %d %s", i1, ib1, i2, ib2)
	}
	pool.QueryRow(ctx, `SELECT count(*) FROM user_invites WHERE tenant_id='itest-id1' AND email='newjoin@idem.example'`).Scan(&n)
	if n != 1 {
		t.Fatalf("invites after replay = %d, want 1", n)
	}
	if fm.n != 1 {
		t.Fatalf("invite emails sent = %d, want 1 (replay must not resend)", fm.n)
	}

	// 8. channel retry: one staged-disabled channel.
	c8a, _ := asKey("itest-id1", "idem-admin", "admin", "/v1/notifications/channels", "k-ch-1", `{"type":"email","target":"alerts@idem.example"}`)
	c8b, _ := asKey("itest-id1", "idem-admin", "admin", "/v1/notifications/channels", "k-ch-1", `{"type":"email","target":"alerts@idem.example"}`)
	pool.QueryRow(ctx, `SELECT count(*) FROM notification_channels WHERE tenant_id='itest-id1' AND target='alerts@idem.example'`).Scan(&n)
	if c8a != 201 || c8b != 201 || n != 1 {
		t.Fatalf("channel create/replay = %d/%d rows=%d, want 201/201 rows=1", c8a, c8b, n)
	}
}

// createDevice server-side role gate: a viewer creating devices was a defect. Covers direct endpoint
// attempts across roles, customer scope and tenant boundaries.
func TestIntegrationCreateDeviceRoles(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-dr1")
	ctx := context.Background()
	pool := s.st.Pool
	clean := func() {
		pool.Exec(ctx, `DELETE FROM idempotency_keys WHERE tenant_id='itest-dr1'`)
		pool.Exec(ctx, `DELETE FROM devices WHERE tenant_id='itest-dr1' AND name LIKE 'Role %'`)
		pool.Exec(ctx, `DELETE FROM users WHERE id LIKE 'dr-%'`)
		pool.Exec(ctx, `DELETE FROM user_customer_scope WHERE tenant_id='itest-dr1'`)
		pool.Exec(ctx, `DELETE FROM customers WHERE id='dr-cust'`)
	}
	clean()
	t.Cleanup(clean)
	pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES
		('dr-admin','itest-dr1','dr-admin@dr.example','A','admin'),
		('dr-op','itest-dr1','dr-op@dr.example','O','operator'),
		('dr-inst','itest-dr1','dr-inst@dr.example','I','installer'),
		('dr-view','itest-dr1','dr-view@dr.example','V','viewer'),
		('dr-scoped','itest-dr1','dr-scoped@dr.example','S','operator')`)
	pool.Exec(ctx, `INSERT INTO customers(id,tenant_id,name) VALUES('dr-cust','itest-dr1','C')`)
	pool.Exec(ctx, `INSERT INTO user_customer_scope(tenant_id,user_id,customer_id) VALUES('itest-dr1','dr-scoped','dr-cust')`)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/devices", s.createDevice)
	h := s.activeUser(s.customerScope(mux))
	body := `{"gateway_id":"itest-dr1-gw","profile":"modbus-tcp","name":"Role %s","config":{}}`
	for _, tc := range []struct {
		user, role string
		want       int
	}{
		{"dr-admin", "admin", 201},
		{"dr-op", "operator", 201},
		{"dr-inst", "installer", 201},
		{"dr-view", "viewer", 403},     // the defect: viewers must not create devices
		{"dr-scoped", "operator", 403}, // customer-scoped: scope middleware refuses all writes
	} {
		w, r := keyRequest("itest-dr1", tc.user, tc.role, "/v1/devices", "", fmt.Sprintf(body, tc.user))
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s (%s): got %d %s, want %d", tc.user, tc.role, w.Code, w.Body.String(), tc.want)
		}
	}
}

// alert-rule creation takes the same key: replay does not duplicate the rule, a changed body is a 409.
func TestIntegrationRuleIdempotency(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-ri1")
	ctx := context.Background()
	pool := s.st.Pool
	clean := func() {
		pool.Exec(ctx, `DELETE FROM idempotency_keys WHERE tenant_id='itest-ri1'`)
		pool.Exec(ctx, `DELETE FROM rules WHERE tenant_id='itest-ri1' AND name LIKE 'IdemRule%'`)
		pool.Exec(ctx, `DELETE FROM users WHERE id='ri-op'`)
	}
	clean()
	t.Cleanup(clean)
	pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('ri-op','itest-ri1','ri-op@ri.example','O','operator')`)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/rules", s.createRule)
	h := s.activeUser(s.customerScope(mux))
	body := `{"name":"IdemRule A","enabled":true,"definition":{"device_id":"itest-ri1-dev","point_id":"temp","op":">","threshold":80,"severity":"warning","message":"IdemRule A"}}`
	asKey := func(key, b string) (int, string) {
		w, r := keyRequest("itest-ri1", "ri-op", "operator", "/v1/rules", key, b)
		h.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	c1, b1 := asKey("k-rule-1", body)
	c2, b2 := asKey("k-rule-1", body)
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM rules WHERE tenant_id='itest-ri1' AND name='IdemRule A'`).Scan(&n)
	if c1 != 201 || c2 != 201 || b1 != b2 || n != 1 {
		t.Fatalf("rule create/replay = %d/%d rows=%d body-eq=%v", c1, c2, n, b1 == b2)
	}
	if c3, _ := asKey("k-rule-1", strings.Replace(body, "IdemRule A", "IdemRule B", 1)); c3 != 409 {
		t.Fatalf("changed payload = %d, want 409", c3)
	}
}
