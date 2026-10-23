package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// Group-wide aggregate reads: /v1/telemetry/group-latest aggregates each
// point's latest value across the transitive members of a group (own members
// plus every descendant group's), one sample per device, with sum/avg/min/max.
// Devices outside the tree never count; mixed units are flagged, not hidden.
func TestIntegrationGroupLatest(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-gl1")
	ctx := context.Background()
	pool := s.st.Pool
	clean := func() {
		for _, q := range []string{
			`DELETE FROM telemetry WHERE tenant_id='itest-gl1'`,
			`DELETE FROM device_group_members WHERE tenant_id='itest-gl1'`,
			`DELETE FROM device_groups WHERE tenant_id='itest-gl1'`,
			`DELETE FROM devices WHERE tenant_id='itest-gl1' AND id LIKE 'itest-gl1-x%'`,
		} {
			pool.Exec(ctx, q)
		}
	}
	clean()
	t.Cleanup(clean)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/groups", s.createGroup)
	mux.HandleFunc("GET /v1/telemetry/group-latest", s.groupLatestTelemetry)

	mk := func(name, parent string) string {
		w := call(mux, "itest-gl1", "admin", "POST", "/v1/groups", fmt.Sprintf(`{"name":%q,"parent_id":%q}`, name, parent))
		if w.Code != 201 {
			t.Fatalf("create %s = %d (%s)", name, w.Code, w.Body.String())
		}
		var out map[string]string
		json.Unmarshal(w.Body.Bytes(), &out)
		return out["id"]
	}
	root := mk("Plant", "")
	child := mk("Line 1", root)
	empty := mk("Empty", "")

	for _, q := range []string{
		`INSERT INTO devices(id,tenant_id,gateway_id,profile,name) VALUES
		 ('itest-gl1-x1','itest-gl1','itest-gl1-gw','modbus-meter','deep device'),
		 ('itest-gl1-x2','itest-gl1','itest-gl1-gw','modbus-meter','root device'),
		 ('itest-gl1-x3','itest-gl1','itest-gl1-gw','modbus-meter','outside device')`,
		`INSERT INTO device_group_members(group_id,tenant_id,device_id) VALUES
		 ('` + child + `','itest-gl1','itest-gl1-x1'),
		 ('` + root + `','itest-gl1','itest-gl1-x2')`,
		// power: x1=10, x2 newest 30 (older 999 must not win), x3=100 outside
		`INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,value,unit,quality,schema_version) VALUES
		 ('gl1','itest-gl1','itest-gl1-gw','itest-gl1-x1','power',now(),10,'kW','measured',1),
		 ('gl2','itest-gl1','itest-gl1-gw','itest-gl1-x2','power',now(),30,'kW','measured',1),
		 ('gl3','itest-gl1','itest-gl1-gw','itest-gl1-x2','power',now()-interval '1 hour',999,'kW','measured',1),
		 ('gl4','itest-gl1','itest-gl1-gw','itest-gl1-x3','power',now(),100,'kW','measured',1),
		 ('gl5','itest-gl1','itest-gl1-gw','itest-gl1-x1','temp',now(),5,'C','measured',1),
		 ('gl6','itest-gl1','itest-gl1-gw','itest-gl1-x1','flow',now(),2,'L/min','measured',1),
		 ('gl7','itest-gl1','itest-gl1-gw','itest-gl1-x2','flow',now(),4,'m3/h','measured',1)`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	get := func(group, agg string) (int, []map[string]any) {
		w := call(mux, "itest-gl1", "viewer", "GET", "/v1/telemetry/group-latest?group_id="+group+"&agg="+agg, "")
		var out []map[string]any
		if w.Code == 200 {
			json.Unmarshal(w.Body.Bytes(), &out)
		}
		return w.Code, out
	}
	point := func(rows []map[string]any, id string) map[string]any {
		for _, r := range rows {
			if r["point_id"] == id {
				return r
			}
		}
		return nil
	}

	// root covers its own member plus the child group's member; never x3
	code, rows := get(root, "sum")
	if code != 200 {
		t.Fatalf("sum = %d", code)
	}
	p := point(rows, "power")
	if p == nil || p["value"].(float64) != 40 || p["devices"].(float64) != 2 {
		t.Fatalf("root power sum = %v, want 40 over 2 devices", p)
	}
	if p["unit"] != "kW" || p["units_mixed"].(bool) {
		t.Fatalf("uniform units misreported: %v", p)
	}
	if tm := point(rows, "temp"); tm == nil || tm["value"].(float64) != 5 || tm["devices"].(float64) != 1 {
		t.Fatalf("single-device point = %v, want 5 over 1 device", tm)
	}
	if f := point(rows, "flow"); f == nil || !f["units_mixed"].(bool) || f["unit"] != "" {
		t.Fatalf("mixed units not flagged: %v", f)
	}
	for agg, want := range map[string]float64{"avg": 20, "min": 10, "max": 30} {
		_, rows := get(root, agg)
		if p := point(rows, "power"); p == nil || p["value"].(float64) != want {
			t.Fatalf("%s power = %v, want %v (latest per device, older 999 ignored)", agg, p, want)
		}
	}
	// the child group alone sees only its own member
	_, rows = get(child, "sum")
	if p := point(rows, "power"); p == nil || p["value"].(float64) != 10 || p["devices"].(float64) != 1 {
		t.Fatalf("child power sum = %v, want 10 over 1 device", p)
	}
	// an empty group aggregates nothing, a stranger gets 404, bad input 400
	if code, rows := get(empty, "sum"); code != 200 || len(rows) != 0 {
		t.Fatalf("empty group = %d %v, want 200 []", code, rows)
	}
	if code, _ := get("no-such-group", "sum"); code != 404 {
		t.Fatalf("unknown group = %d, want 404", code)
	}
	if code, _ := get(root, "median"); code != 400 {
		t.Fatalf("bad agg = %d, want 400", code)
	}
	if w := call(mux, "itest-gl1", "viewer", "GET", "/v1/telemetry/group-latest?agg=sum", ""); w.Code != 400 {
		t.Fatalf("missing group_id = %d, want 400", w.Code)
	}

	// a customer-scoped viewer aggregates only their own subtree's devices:
	// x2 moves under customer C, x1 stays unscoped, carl sees only x2
	scoped := http.NewServeMux()
	scoped.HandleFunc("POST /v1/customers", s.createCustomer)
	scoped.HandleFunc("PUT /v1/customers/{id}/users/{user}", s.setCustomerUser)
	scoped.HandleFunc("PUT /v1/devices/{id}/customer", s.setDeviceCustomer)
	scoped.HandleFunc("GET /v1/telemetry/group-latest", s.groupLatestTelemetry)
	h := s.customerScope(scoped)
	w := callAs(h, "itest-gl1", "admin1", "admin", "POST", "/v1/customers", `{"name":"C"}`)
	if w.Code != 201 {
		t.Fatalf("create customer: %d %s", w.Code, w.Body)
	}
	var cust struct{ ID string }
	json.Unmarshal(w.Body.Bytes(), &cust)
	for _, step := range []*struct{ code int }{&struct{ code int }{}} {
		_ = step
	}
	if w := callAs(h, "itest-gl1", "admin1", "admin", "PUT", "/v1/devices/itest-gl1-x2/customer", `{"customer_id":"`+cust.ID+`"}`); w.Code != 204 {
		t.Fatalf("assign x2: %d %s", w.Code, w.Body)
	}
	if w := callAs(h, "itest-gl1", "admin1", "admin", "PUT", "/v1/customers/"+cust.ID+"/users/carl", ``); w.Code != 204 {
		t.Fatalf("scope carl: %d %s", w.Code, w.Body)
	}
	w = callAs(h, "itest-gl1", "carl", "viewer", "GET", "/v1/telemetry/group-latest?group_id="+root+"&agg=sum", "")
	if w.Code != 200 {
		t.Fatalf("scoped sum = %d", w.Code)
	}
	var srows []map[string]any
	json.Unmarshal(w.Body.Bytes(), &srows)
	if p := point(srows, "power"); p == nil || p["value"].(float64) != 30 || p["devices"].(float64) != 1 {
		t.Fatalf("scoped power sum = %v, want 30 over 1 device (x1 out of scope)", p)
	}
	if tm := point(srows, "temp"); tm != nil {
		t.Fatalf("scoped viewer saw out-of-scope device's point: %v", tm)
	}
}
