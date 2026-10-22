package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/rules"
)

// Nested groups: write-time shape checks (unknown parent 404, self/cycle/depth
// 400) and transitive coverage everywhere a group scope is accepted - rule
// evaluation, the devices fleet filter and the alert list. Reparenting moves
// coverage; deleting a parent lifts children to top level.
func TestIntegrationNestedGroups(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-ng1")
	ctx := context.Background()
	pool := s.st.Pool
	clean := func() {
		for _, q := range []string{
			`DELETE FROM alerts WHERE tenant_id='itest-ng1'`,
			`DELETE FROM rules WHERE tenant_id='itest-ng1'`,
			`DELETE FROM device_group_members WHERE tenant_id='itest-ng1'`,
			`DELETE FROM device_groups WHERE tenant_id='itest-ng1'`,
			`DELETE FROM devices WHERE tenant_id='itest-ng1' AND id LIKE 'itest-ng1-x%'`,
		} {
			pool.Exec(ctx, q)
		}
	}
	clean()
	t.Cleanup(clean)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/groups", s.createGroup)
	mux.HandleFunc("PUT /v1/groups/{id}/parent", s.setGroupParent)
	mux.HandleFunc("GET /v1/devices", s.listDevices)
	mux.HandleFunc("GET /v1/alerts", s.listAlerts)

	mk := func(name, parent string) string {
		body := fmt.Sprintf(`{"name":%q,"parent_id":%q}`, name, parent)
		w := call(mux, "itest-ng1", "admin", "POST", "/v1/groups", body)
		if w.Code != 201 {
			t.Fatalf("create %s = %d (%s)", name, w.Code, w.Body.String())
		}
		var out map[string]string
		json.Unmarshal(w.Body.Bytes(), &out)
		return out["id"]
	}
	root := mk("Plant", "")
	line1 := mk("Line 1", root)
	cellA := mk("Cell A", line1)

	if w := call(mux, "itest-ng1", "admin", "POST", "/v1/groups", `{"name":"Orphan","parent_id":"no-such-group"}`); w.Code != 404 {
		t.Fatalf("unknown parent = %d, want 404", w.Code)
	}
	if w := call(mux, "itest-ng1", "admin", "PUT", "/v1/groups/"+root+"/parent", `{"parent_id":"`+root+`"}`); w.Code != 400 {
		t.Fatalf("self parent = %d, want 400", w.Code)
	}
	if w := call(mux, "itest-ng1", "admin", "PUT", "/v1/groups/"+root+"/parent", `{"parent_id":"`+cellA+`"}`); w.Code != 400 {
		t.Fatalf("cycle = %d, want 400", w.Code)
	}
	if w := call(mux, "itest-ng1", "admin", "PUT", "/v1/groups/"+root+"/parent", `{"parent_id":"no-such-group"}`); w.Code != 404 {
		t.Fatalf("reparent to unknown = %d, want 404", w.Code)
	}
	// depth cap: a chain of 8 is fine, the 9th level is refused
	parent := cellA          // root, line1, cellA are already 3 deep
	for i := 0; i < 5; i++ { // extend to exactly 8 levels
		parent = mk(fmt.Sprintf("L%d", i+4), parent)
	}
	if w := call(mux, "itest-ng1", "admin", "POST", "/v1/groups", `{"name":"Too deep","parent_id":"`+parent+`"}`); w.Code != 400 {
		t.Fatalf("9th level = %d, want 400", w.Code)
	}

	// devices: one in the grandchild, one top-level group, one outside
	for _, q := range []string{
		`INSERT INTO devices(id,tenant_id,gateway_id,profile,name) VALUES
		 ('itest-ng1-x1','itest-ng1','itest-ng1-gw','modbus-tcp','deep device'),
		 ('itest-ng1-x2','itest-ng1','itest-ng1-gw','modbus-tcp','line device'),
		 ('itest-ng1-x3','itest-ng1','itest-ng1-gw','modbus-tcp','outside device')`,
		`INSERT INTO device_group_members(group_id,tenant_id,device_id) VALUES
		 ('` + cellA + `','itest-ng1','itest-ng1-x1'),
		 ('` + line1 + `','itest-ng1','itest-ng1-x2')`,
		`INSERT INTO rules(id,tenant_id,name,definition,enabled,created_by) VALUES
		 ('ng-rule','itest-ng1','plant temp','{"point_id":"temp","op":">","threshold":50,"severity":"warning","group_id":"` + root + `"}',true,'test-user')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	// rule scoped to the root covers members of child and grandchild groups
	fired := func(device string) (n int) {
		pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE rule_id='ng-rule' AND device_id=$1`, device).Scan(&n)
		return
	}
	for _, dev := range []string{"itest-ng1-x1", "itest-ng1-x2", "itest-ng1-x3"} {
		rules.Evaluate(ctx, pool, nil, "itest-ng1", dev, "temp", 99)
	}
	if fired("itest-ng1-x1") != 1 || fired("itest-ng1-x2") != 1 {
		t.Fatal("root-scoped rule did not fire for child and grandchild group members")
	}
	if fired("itest-ng1-x3") != 0 {
		t.Fatal("root-scoped rule fired for a device outside the tree")
	}

	devIDs := func(path string) string {
		w := call(mux, "itest-ng1", "admin", "GET", path, "")
		if w.Code != 200 {
			t.Fatalf("%s = %d", path, w.Code)
		}
		return w.Body.String()
	}
	if got := devIDs("/v1/devices?group_id=" + root); !strings.Contains(got, "itest-ng1-x1") || !strings.Contains(got, "itest-ng1-x2") || strings.Contains(got, "itest-ng1-x3") {
		t.Fatalf("devices?group_id=root = %s", got)
	}
	if got := devIDs("/v1/devices?group_id=" + cellA); !strings.Contains(got, "itest-ng1-x1") || strings.Contains(got, "itest-ng1-x2") {
		t.Fatalf("devices?group_id=cellA = %s", got)
	}
	if got := devIDs("/v1/alerts?group_id=" + root); !strings.Contains(got, "itest-ng1-x1") || !strings.Contains(got, "itest-ng1-x2") {
		t.Fatalf("alerts?group_id=root = %s", got)
	}
	if got := devIDs("/v1/alerts?group_id=" + line1); strings.Contains(got, "itest-ng1-x3") {
		t.Fatalf("alerts?group_id=line1 leaked an outside device: %s", got)
	}

	// reparenting moves coverage: x3 joins the tree under cellA, root now covers it
	loose := mk("Loose", "")
	pool.Exec(ctx, `INSERT INTO device_group_members(group_id,tenant_id,device_id) VALUES ($1,'itest-ng1','itest-ng1-x3')`, loose)
	if w := call(mux, "itest-ng1", "admin", "PUT", "/v1/groups/"+loose+"/parent", `{"parent_id":"`+cellA+`"}`); w.Code != 200 {
		t.Fatalf("reparent = %d", w.Code)
	}
	if got := devIDs("/v1/devices?group_id=" + root); !strings.Contains(got, "itest-ng1-x3") {
		t.Fatalf("after reparent root should cover x3: %s", got)
	}

	// deleting a parent lifts its children to top level (SET NULL), never deletes them
	if _, err := pool.Exec(ctx, `DELETE FROM device_groups WHERE tenant_id='itest-ng1' AND id=$1`, line1); err != nil {
		t.Fatal(err)
	}
	var lifted *string
	pool.QueryRow(ctx, `SELECT parent_id FROM device_groups WHERE tenant_id='itest-ng1' AND id=$1`, cellA).Scan(&lifted)
	if lifted != nil {
		t.Fatalf("child group not lifted to top level, parent_id=%v", *lifted)
	}
	if got := devIDs("/v1/devices?group_id=" + cellA); !strings.Contains(got, "itest-ng1-x1") {
		t.Fatalf("child group lost its members when its parent was deleted: %s", got)
	}
}
