package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestIntegrationQuotas(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-q1")
	ctx := context.Background()
	pool := s.st.Pool
	clean := func() {
		pool.Exec(ctx, `DELETE FROM tenant_quotas WHERE tenant_id='itest-q1'`)
		pool.Exec(ctx, `DELETE FROM user_invites WHERE tenant_id='itest-q1'`)
		pool.Exec(ctx, `DELETE FROM api_keys WHERE tenant_id='itest-q1'`)
		pool.Exec(ctx, `DELETE FROM devices WHERE tenant_id='itest-q1' AND id LIKE 'q-%'`)
		pool.Exec(ctx, `DELETE FROM customers WHERE tenant_id='itest-q1'`)
		pool.Exec(ctx, `DELETE FROM audit_log WHERE tenant_id='itest-q1'`)
		pool.Exec(ctx, `DELETE FROM users WHERE tenant_id='itest-q1'`)
	}
	clean()
	t.Cleanup(clean)
	pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('q-admin','itest-q1','q-admin@q-test.example','Q','admin')`)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/devices", s.createDevice)
	mux.HandleFunc("POST /v1/customers", s.createCustomer)
	mux.HandleFunc("POST /v1/api-keys", s.createAPIKey)
	mux.HandleFunc("POST /v1/users", s.createUser)
	mux.HandleFunc("POST /v1/users/invites", s.createInvite)
	mux.HandleFunc("GET /v1/usage", s.usage)
	h := s.customerScope(mux)
	adm := func(method, path, body string) *http.Response {
		return callAs(h, "itest-q1", "q-admin", "admin", method, path, body).Result()
	}
	pool.Exec(ctx, `INSERT INTO tenant_quotas(tenant_id,max_devices,max_customers,max_api_keys,max_users) VALUES('itest-q1',2,1,1,3)`)

	// devices: two fit, the third is refused with a clear message
	dev := func() int {
		return adm("POST", "/v1/devices", `{"gateway_id":"itest-q1-gw","profile":"modbus-tcp","name":"d"}`).StatusCode
	}
	// a seeded device may already exist for the tenant: count what is there and fill the rest
	var have int
	pool.QueryRow(ctx, `SELECT count(*) FROM devices WHERE tenant_id='itest-q1'`).Scan(&have)
	pool.Exec(ctx, `UPDATE tenant_quotas SET max_devices=$1 WHERE tenant_id='itest-q1'`, have+2)
	if dev() != 201 || dev() != 201 {
		t.Fatal("devices within the limit were refused")
	}
	if c := dev(); c != 409 {
		t.Fatalf("device over the limit = %d, want 409", c)
	}
	// customers
	if adm("POST", "/v1/customers", `{"name":"C1"}`).StatusCode != 201 || adm("POST", "/v1/customers", `{"name":"C2"}`).StatusCode != 409 {
		t.Fatal("customer limit")
	}
	// api keys: a revoked key frees its slot
	if adm("POST", "/v1/api-keys", `{"name":"k1","role":"viewer","expires_in_days":30}`).StatusCode != 201 {
		t.Fatal("first key")
	}
	if c := adm("POST", "/v1/api-keys", `{"name":"k2","role":"viewer","expires_in_days":30}`).StatusCode; c != 409 {
		t.Fatalf("second key = %d, want 409", c)
	}
	pool.Exec(ctx, `UPDATE api_keys SET revoked_at=now() WHERE tenant_id='itest-q1'`)
	if c := adm("POST", "/v1/api-keys", `{"name":"k3","role":"viewer","expires_in_days":30}`).StatusCode; c != 201 {
		t.Fatalf("key after revoking = %d", c)
	}
	// users: admin + one invite + one user = 3; a fourth is refused. A pending invite holds its seat.
	t.Setenv("LOCAL_LOGIN", "1")
	if c := adm("POST", "/v1/users/invites", `{"email":"inv1@q-test.example","role":"viewer"}`).StatusCode; c != 201 {
		t.Fatalf("invite = %d", c)
	}
	if c := adm("POST", "/v1/users", `{"email":"u2@q-test.example","display_name":"U2","role":"viewer","password":"a-long-enough-password"}`).StatusCode; c != 201 {
		t.Fatalf("user within limit = %d", c)
	}
	if c := adm("POST", "/v1/users/invites", `{"email":"inv2@q-test.example","role":"viewer"}`).StatusCode; c != 409 {
		t.Fatalf("invite over the limit = %d, want 409", c)
	}
	if c := adm("POST", "/v1/users", `{"email":"u3@q-test.example","display_name":"U3","role":"viewer","password":"a-long-enough-password"}`).StatusCode; c != 409 {
		t.Fatalf("user over the limit = %d, want 409", c)
	}
	// usage reports it, and does not leak other tenants
	body := callAs(h, "itest-q1", "q-admin", "admin", "GET", "/v1/usage", "").Body.String()
	if !strings.Contains(body, `"resource":"customers","used":1,"limit":1`) {
		t.Fatalf("usage: %s", body)
	}
	if c := callAs(h, "itest-q1", "q-admin", "viewer", "GET", "/v1/usage", "").Code; c != 403 {
		t.Fatalf("usage as viewer = %d", c)
	}
	// the database trigger holds under concurrency, independent of the handler check
	pool.Exec(ctx, `DELETE FROM devices WHERE tenant_id='itest-q1' AND id LIKE 'q-%'`)
	pool.Exec(ctx, `UPDATE devices SET id=id WHERE false`)
	pool.QueryRow(ctx, `SELECT count(*) FROM devices WHERE tenant_id='itest-q1'`).Scan(&have)
	pool.Exec(ctx, `UPDATE tenant_quotas SET max_devices=$1 WHERE tenant_id='itest-q1'`, have+3)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := pool.Exec(ctx, fmt.Sprintf(`INSERT INTO devices(id,tenant_id,gateway_id,profile,name,config) VALUES('q-race-%d','itest-q1','itest-q1-gw','modbus-tcp','r','{}')`, i))
			if err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if ok != 3 {
		t.Fatalf("concurrent inserts that succeeded = %d, want exactly 3", ok)
	}
	// no limit row means unlimited
	pool.Exec(ctx, `DELETE FROM tenant_quotas WHERE tenant_id='itest-q1'`)
	if c := dev(); c != 201 {
		t.Fatalf("unlimited tenant device create = %d", c)
	}
}
