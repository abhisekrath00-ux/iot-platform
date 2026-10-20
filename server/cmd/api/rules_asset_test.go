package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/rules"
)

// An asset-scoped threshold rule fires for every device attached to the asset
// or below it in the tree, and for no device outside it.
func TestIntegrationAssetScopedRules(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-ra1")
	ctx := context.Background()
	pool := s.st.Pool
	clean := func() {
		for _, q := range []string{
			`DELETE FROM alerts WHERE tenant_id='itest-ra1'`,
			`DELETE FROM rules WHERE tenant_id='itest-ra1'`,
			`DELETE FROM devices WHERE tenant_id='itest-ra1' AND id LIKE 'itest-ra1-x%'`,
			`DELETE FROM assets WHERE tenant_id='itest-ra1'`,
		} {
			pool.Exec(ctx, q)
		}
	}
	clean()
	t.Cleanup(clean)
	for _, q := range []string{
		// tree: site a1 > line a2; machine x1 on the line, machine x2 directly on the site, x3 on no asset
		`INSERT INTO assets(id,tenant_id,parent_id,name,kind) VALUES
		 ('ra-a1','itest-ra1',NULL,'Site A','site'),
		 ('ra-a2','itest-ra1','ra-a1','Line 1','line')`,
		`INSERT INTO devices(id,tenant_id,gateway_id,profile,name,asset_id) VALUES
		 ('itest-ra1-x1','itest-ra1','itest-ra1-gw','modbus-tcp','Line motor','ra-a2'),
		 ('itest-ra1-x2','itest-ra1','itest-ra1-gw','modbus-tcp','Site meter','ra-a1'),
		 ('itest-ra1-x3','itest-ra1','itest-ra1-gw','modbus-tcp','Loose sensor',NULL)`,
		`INSERT INTO rules(id,tenant_id,name,definition,enabled,created_by) VALUES
		 ('ra-site','itest-ra1','site high temp','{"point_id":"temp","op":">","threshold":50,"severity":"warning","asset_id":"ra-a1"}',true,'test-user'),
		 ('ra-line','itest-ra1','line high temp','{"point_id":"temp","op":">","threshold":50,"severity":"warning","asset_id":"ra-a2"}',true,'test-user')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	fired := func(rule, device string) (n int) {
		pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE rule_id=$1 AND device_id=$2`, rule, device).Scan(&n)
		return
	}
	for _, dev := range []string{"itest-ra1-x1", "itest-ra1-x2", "itest-ra1-x3", "itest-ra1-dev"} {
		rules.Evaluate(ctx, pool, nil, "itest-ra1", dev, "temp", 99)
	}
	// site rule: x1 (below on line) and x2 (on the site) fire; x3 and the unassigned seed device do not
	if fired("ra-site", "itest-ra1-x1") != 1 || fired("ra-site", "itest-ra1-x2") != 1 {
		t.Fatal("site rule did not fire for devices on the site and below")
	}
	if fired("ra-site", "itest-ra1-x3") != 0 || fired("ra-site", "itest-ra1-dev") != 0 {
		t.Fatal("site rule fired for a device outside the asset tree")
	}
	// line rule: only x1
	if fired("ra-line", "itest-ra1-x1") != 1 {
		t.Fatal("line rule did not fire for the device on the line")
	}
	if fired("ra-line", "itest-ra1-x2") != 0 {
		t.Fatal("line rule fired for a device only on the parent site")
	}
	// a rule naming an unknown asset simply never fires (creation rejects it; a
	// stale definition after an asset delete must not error or match everything)
	pool.Exec(ctx, `DELETE FROM assets WHERE tenant_id='itest-ra1' AND id='ra-a2'`)
	rules.Evaluate(ctx, pool, nil, "itest-ra1", "itest-ra1-x1", "temp", 150)
	if fired("ra-line", "itest-ra1-x1") != 1 {
		t.Fatal("line rule fired again after its asset was deleted")
	}
}

func TestAssetRuleValidate(t *testing.T) {
	base := rules.Definition{PointID: "temp", Op: ">", Threshold: 50, Severity: "warning"}
	d := base
	d.AssetID = "a1"
	if err := d.Validate(); err != nil {
		t.Fatalf("asset rule rejected: %v", err)
	}
	d = base
	d.AssetID = "a1"
	d.DeviceID = "dev-1"
	if err := d.Validate(); err == nil {
		t.Fatal("asset_id combined with device_id accepted")
	}
	d = base
	d.Kind = "kpi_band"
	d.KPIID = "k1"
	mx := 5.0
	d.Max = &mx
	d.AssetID = "a1"
	if err := d.Validate(); err == nil {
		t.Fatal("asset_id on a kpi_band rule accepted")
	}
	d = base
	d.Kind = "forecast_limit"
	d.DeviceID = ""
	d.AssetID = "a1"
	d.HorizonHours = 4
	if err := d.Validate(); err == nil {
		t.Fatal("asset_id on a forecast_limit rule accepted")
	}
}

// The API rejects a rule naming an asset that does not exist in the workspace,
// and accepts one that does.
func TestIntegrationAssetRuleUnknownAsset(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-ra2")
	ctx := context.Background()
	pool := s.st.Pool
	pool.Exec(ctx, `DELETE FROM rules WHERE tenant_id='itest-ra2'`)
	pool.Exec(ctx, `DELETE FROM assets WHERE tenant_id='itest-ra2'`)
	if _, err := pool.Exec(ctx, `INSERT INTO assets(id,tenant_id,parent_id,name,kind) VALUES('ra2-a1','itest-ra2',NULL,'Site','site')`); err != nil {
		t.Fatal(err)
	}
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/rules", s.createRule)
	post := func(def string) int {
		b, _ := json.Marshal(map[string]any{"name": "r", "definition": json.RawMessage(def), "enabled": false})
		return call(api, "itest-ra2", "operator", "POST", "/v1/rules", string(b)).Code
	}
	if c := post(`{"point_id":"temp","op":">","threshold":1,"severity":"info","asset_id":"no-such-asset"}`); c != 404 {
		t.Fatalf("unknown asset: got %d, want 404", c)
	}
	if c := post(`{"point_id":"temp","op":">","threshold":1,"severity":"info","asset_id":"ra2-a1"}`); c != 201 {
		t.Fatalf("known asset: got %d, want 201", c)
	}
	if c := post(`{"point_id":"temp","op":">","threshold":1,"severity":"info","asset_id":"ra2-a1","device_id":"itest-ra2-dev"}`); c != 400 {
		t.Fatalf("asset+device: got %d, want 400", c)
	}
}
