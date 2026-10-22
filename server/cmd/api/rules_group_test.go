package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/rules"
)

// A group-scoped threshold rule fires for every device that is a member of the
// group, for no device outside it, and follows membership changes. A rule
// whose group was deleted never fires (creation rejects unknown groups; a
// stale definition must not error or match everything).
func TestIntegrationGroupScopedRules(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-rg1")
	ctx := context.Background()
	pool := s.st.Pool
	clean := func() {
		for _, q := range []string{
			`DELETE FROM alerts WHERE tenant_id='itest-rg1'`,
			`DELETE FROM rules WHERE tenant_id='itest-rg1'`,
			`DELETE FROM device_group_members WHERE tenant_id='itest-rg1'`,
			`DELETE FROM device_groups WHERE tenant_id='itest-rg1'`,
			`DELETE FROM devices WHERE tenant_id='itest-rg1' AND id LIKE 'itest-rg1-x%'`,
		} {
			pool.Exec(ctx, q)
		}
	}
	clean()
	t.Cleanup(clean)
	for _, q := range []string{
		`INSERT INTO device_groups(id,tenant_id,name) VALUES ('rg-g1','itest-rg1','Compressors')`,
		`INSERT INTO devices(id,tenant_id,gateway_id,profile,name) VALUES
		 ('itest-rg1-x1','itest-rg1','itest-rg1-gw','modbus-tcp','Compressor 1'),
		 ('itest-rg1-x2','itest-rg1','itest-rg1-gw','modbus-tcp','Compressor 2'),
		 ('itest-rg1-x3','itest-rg1','itest-rg1-gw','modbus-tcp','Loose sensor')`,
		`INSERT INTO device_group_members(group_id,tenant_id,device_id) VALUES
		 ('rg-g1','itest-rg1','itest-rg1-x1'),
		 ('rg-g1','itest-rg1','itest-rg1-x2')`,
		`INSERT INTO rules(id,tenant_id,name,definition,enabled,created_by) VALUES
		 ('rg-rule','itest-rg1','group high temp','{"point_id":"temp","op":">","threshold":50,"severity":"warning","group_id":"rg-g1"}',true,'test-user')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	fired := func(rule, device string) (n int) {
		pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE rule_id=$1 AND device_id=$2`, rule, device).Scan(&n)
		return
	}
	for _, dev := range []string{"itest-rg1-x1", "itest-rg1-x2", "itest-rg1-x3", "itest-rg1-dev"} {
		rules.Evaluate(ctx, pool, nil, "itest-rg1", dev, "temp", 99)
	}
	if fired("rg-rule", "itest-rg1-x1") != 1 || fired("rg-rule", "itest-rg1-x2") != 1 {
		t.Fatal("group rule did not fire for both group members")
	}
	if fired("rg-rule", "itest-rg1-x3") != 0 || fired("rg-rule", "itest-rg1-dev") != 0 {
		t.Fatal("group rule fired for a device outside the group")
	}
	// membership change: a device added later is covered without touching the rule
	pool.Exec(ctx, `INSERT INTO device_group_members(group_id,tenant_id,device_id) VALUES ('rg-g1','itest-rg1','itest-rg1-x3')`)
	rules.Evaluate(ctx, pool, nil, "itest-rg1", "itest-rg1-x3", "temp", 99)
	if fired("rg-rule", "itest-rg1-x3") != 1 {
		t.Fatal("group rule did not cover a device added to the group after the rule was created")
	}
	// deleted group: the stale definition fires for nobody, without error
	pool.Exec(ctx, `DELETE FROM device_groups WHERE tenant_id='itest-rg1' AND id='rg-g1'`)
	rules.Evaluate(ctx, pool, nil, "itest-rg1", "itest-rg1-x1", "temp", 150)
	if fired("rg-rule", "itest-rg1-x1") != 1 {
		t.Fatal("group rule fired again after its group was deleted")
	}
}

// API-level validation of group-scoped rules: unknown group 404, forbidden
// combinations 400, a valid group rule 201.
func TestIntegrationGroupRuleAPI(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-rg2")
	ctx := context.Background()
	pool := s.st.Pool
	clean := func() {
		for _, q := range []string{
			`DELETE FROM rules WHERE tenant_id='itest-rg2'`,
			`DELETE FROM device_groups WHERE tenant_id='itest-rg2'`,
		} {
			pool.Exec(ctx, q)
		}
	}
	clean()
	t.Cleanup(clean)
	if _, err := pool.Exec(ctx, `INSERT INTO device_groups(id,tenant_id,name) VALUES ('rg2-g1','itest-rg2','Compressors')`); err != nil {
		t.Fatal(err)
	}
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/rules", s.createRule)
	if w := call(api, "itest-rg2", "admin", "POST", "/v1/rules", `{"name":"x","enabled":true,"definition":{"point_id":"temp","op":">","threshold":1,"severity":"info","group_id":"no-such-group"}}`); w.Code != 404 {
		t.Fatalf("unknown group = %d, want 404", w.Code)
	}
	if w := call(api, "itest-rg2", "admin", "POST", "/v1/rules", `{"name":"x","enabled":true,"definition":{"point_id":"temp","op":">","threshold":1,"severity":"info","group_id":"rg2-g1","device_id":"dev-1"}}`); w.Code != 400 {
		t.Fatalf("group + device = %d, want 400", w.Code)
	}
	if w := call(api, "itest-rg2", "admin", "POST", "/v1/rules", `{"name":"x","enabled":true,"definition":{"point_id":"temp","op":">","threshold":1,"severity":"info","group_id":"rg2-g1","asset_id":"a1"}}`); w.Code != 400 {
		t.Fatalf("group + asset = %d, want 400", w.Code)
	}
	if w := call(api, "itest-rg2", "admin", "POST", "/v1/rules", `{"name":"x","enabled":true,"definition":{"point_id":"temp","op":">","threshold":1,"severity":"info","group_id":"rg2-g1"}}`); w.Code != 201 {
		t.Fatalf("valid group rule = %d, want 201", w.Code)
	}
}

func TestGroupRuleValidate(t *testing.T) {
	base := rules.Definition{PointID: "temp", Op: ">", Threshold: 50, Severity: "warning"}
	d := base
	d.GroupID = "g1"
	if err := d.Validate(); err != nil {
		t.Fatalf("group-scoped rule rejected: %v", err)
	}
	d = base
	d.GroupID, d.DeviceID = "g1", "dev-1"
	if err := d.Validate(); err == nil {
		t.Fatal("group_id combined with device_id accepted")
	}
	d = base
	d.GroupID, d.AssetID = "g1", "a1"
	if err := d.Validate(); err == nil {
		t.Fatal("group_id combined with asset_id accepted")
	}
	d = base
	d.GroupID, d.Kind, d.KPIID = "g1", "kpi_band", "k"
	if err := d.Validate(); err == nil {
		t.Fatal("group_id on a kpi_band rule accepted")
	}
}
