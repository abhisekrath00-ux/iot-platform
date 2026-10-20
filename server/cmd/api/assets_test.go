package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/rules"
)

func TestIntegrationAssetHierarchy(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-as1")
	seed(t, s, "itest-as2")
	ctx := t.Context()
	clean := func() {
		for _, tn := range []string{"itest-as1", "itest-as2"} {
			s.st.Pool.Exec(ctx, `UPDATE devices SET asset_id=NULL WHERE tenant_id=$1`, tn)
			s.st.Pool.Exec(ctx, `DELETE FROM assets WHERE tenant_id=$1 AND parent_id IS NOT NULL`, tn)
			s.st.Pool.Exec(ctx, `DELETE FROM assets WHERE tenant_id=$1`, tn)
		}
	}
	clean()
	t.Cleanup(clean)
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/assets", s.listAssets)
	api.HandleFunc("POST /v1/assets", s.createAsset)
	api.HandleFunc("DELETE /v1/assets/{id}", s.deleteAsset)
	api.HandleFunc("PUT /v1/devices/{id}/asset", s.setDeviceAsset)
	create := func(tenant, body string) (int, string) {
		w := call(api, tenant, "operator", "POST", "/v1/assets", body)
		var o map[string]any
		json.Unmarshal(w.Body.Bytes(), &o)
		id, _ := o["id"].(string)
		return w.Code, id
	}
	code, plant := create("itest-as1", `{"name":"Plant 1","kind":"plant"}`)
	if code != 201 {
		t.Fatalf("root %d", code)
	}
	code, line := create("itest-as1", `{"name":"Line A","kind":"line","parent_id":"`+plant+`"}`)
	if code != 201 {
		t.Fatalf("child %d", code)
	}
	if c, _ := create("itest-as2", `{"name":"x","parent_id":"`+plant+`"}`); c != 404 {
		t.Fatalf("cross-tenant parent %d", c)
	}
	if c, _ := create("itest-as1", `{"name":"<b>"}`); c != 400 {
		t.Fatalf("bad name %d", c)
	}
	// depth cap
	parent := line
	for i := 0; i < maxAssetDepth+2; i++ {
		c, id := create("itest-as1", `{"name":"deep","parent_id":"`+parent+`"}`)
		if c == 400 {
			break
		}
		if c != 201 {
			t.Fatalf("deep %d", c)
		}
		parent = id
		if i == maxAssetDepth+1 {
			t.Fatal("depth cap never triggered")
		}
	}
	// attach device, then deletion of busy asset is refused
	if w := call(api, "itest-as1", "operator", "PUT", "/v1/devices/itest-as1-dev/asset", `{"asset_id":"`+line+`"}`); w.Code != 200 {
		t.Fatalf("attach %d %s", w.Code, w.Body.String())
	}
	if w := call(api, "itest-as2", "operator", "PUT", "/v1/devices/itest-as1-dev/asset", `{"asset_id":"`+line+`"}`); w.Code != 404 {
		t.Fatalf("cross-tenant attach %d", w.Code)
	}
	if w := call(api, "itest-as1", "operator", "DELETE", "/v1/assets/"+line, ""); w.Code != 409 {
		t.Fatalf("delete busy %d", w.Code)
	}
	w := call(api, "itest-as1", "viewer", "GET", "/v1/assets", "")
	var list []map[string]any
	json.Unmarshal(w.Body.Bytes(), &list)
	var lineDevices float64 = -1
	for _, a := range list {
		if a["id"] == line {
			lineDevices = a["devices"].(float64)
		}
	}
	if lineDevices != 1 {
		t.Fatalf("device count on Line A = %v", lineDevices)
	}
	call(api, "itest-as1", "operator", "PUT", "/v1/devices/itest-as1-dev/asset", `{"asset_id":null}`)
	if call(api, "itest-as1", "viewer", "POST", "/v1/assets", `{"name":"v"}`).Code != 403 {
		t.Fatal("viewer created asset")
	}
}

func TestIntegrationDeviceFilterByAsset(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-af1")
	seed(t, s, "itest-af2")
	ctx := t.Context()
	clean := func() {
		for _, tn := range []string{"itest-af1", "itest-af2"} {
			s.st.Pool.Exec(ctx, `UPDATE devices SET asset_id=NULL WHERE tenant_id=$1`, tn)
			s.st.Pool.Exec(ctx, `DELETE FROM assets WHERE tenant_id=$1 AND parent_id IS NOT NULL`, tn)
			s.st.Pool.Exec(ctx, `DELETE FROM assets WHERE tenant_id=$1`, tn)
		}
	}
	clean()
	t.Cleanup(clean)
	s.st.Pool.Exec(ctx, `INSERT INTO assets(id,tenant_id,parent_id,name,kind) VALUES('af-plant','itest-af1',NULL,'P','plant'),('af-other','itest-af2',NULL,'O','plant')`)
	s.st.Pool.Exec(ctx, `INSERT INTO assets(id,tenant_id,parent_id,name,kind) VALUES('af-line','itest-af1','af-plant','L','line')`)
	s.st.Pool.Exec(ctx, `UPDATE devices SET asset_id='af-line' WHERE id='itest-af1-dev'`)
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/devices", s.listDevices)
	ids := func(tenant, q string) []string {
		w := call(api, tenant, "viewer", "GET", "/v1/devices"+q, "")
		var l []map[string]any
		json.Unmarshal(w.Body.Bytes(), &l)
		var out []string
		for _, d := range l {
			out = append(out, d["id"].(string))
		}
		return out
	}
	if got := ids("itest-af1", "?asset_id=af-plant"); len(got) != 1 || got[0] != "itest-af1-dev" {
		t.Fatalf("descendant match: %v", got)
	}
	if got := ids("itest-af1", "?asset_id=af-other"); len(got) != 0 {
		t.Fatalf("other tenant's asset must match nothing: %v", got)
	}
	s.st.Pool.Exec(ctx, `DELETE FROM alerts WHERE tenant_id='itest-af1' AND id LIKE 'af-al%'`)
	s.st.Pool.Exec(ctx, `INSERT INTO alerts(id,tenant_id,severity,message,device_id) VALUES('af-al1','itest-af1','warning','on line','itest-af1-dev'),('af-al2','itest-af1','warning','no device',NULL)`)
	t.Cleanup(func() { s.st.Pool.Exec(ctx, `DELETE FROM alerts WHERE tenant_id='itest-af1' AND id LIKE 'af-al%'`) })
	api.HandleFunc("GET /v1/alerts", s.listAlerts)
	alertIDs := func(tenant, q string) int {
		w := call(api, tenant, "viewer", "GET", "/v1/alerts"+q, "")
		var l []map[string]any
		json.Unmarshal(w.Body.Bytes(), &l)
		return len(l)
	}
	if n := alertIDs("itest-af1", "?asset_id=af-plant"); n != 1 {
		t.Fatalf("alerts under asset: %d", n)
	}
	if n := alertIDs("itest-af2", "?asset_id=af-plant"); n != 0 {
		t.Fatalf("cross-tenant alerts: %d", n)
	}
	s.st.Pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('af-user','itest-af1','af@x.local','af','admin') ON CONFLICT DO NOTHING`)
	s.st.Pool.Exec(ctx, `DELETE FROM rules WHERE tenant_id='itest-af1'`)
	s.st.Pool.Exec(ctx, `INSERT INTO rules(id,tenant_id,name,definition,enabled,created_by) VALUES
	  ('af-r1','itest-af1','on line','{"device_id":"itest-af1-dev","point_id":"temp","op":">","threshold":1,"severity":"info"}',true,'af-user'),
	  ('af-r2','itest-af1','any device','{"point_id":"temp","op":">","threshold":1,"severity":"info"}',true,'af-user')`)
	t.Cleanup(func() { s.st.Pool.Exec(ctx, `DELETE FROM rules WHERE tenant_id='itest-af1'`) })
	api.HandleFunc("GET /v1/rules", s.listRules)
	ruleCount := func(tenant, q string) int {
		w := call(api, tenant, "viewer", "GET", "/v1/rules"+q, "")
		var l []map[string]any
		json.Unmarshal(w.Body.Bytes(), &l)
		return len(l)
	}
	if n := ruleCount("itest-af1", "?asset_id=af-plant"); n != 1 {
		t.Fatalf("rules under asset: %d", n)
	}
	if n := ruleCount("itest-af2", "?asset_id=af-plant"); n != 0 {
		t.Fatalf("cross-tenant rules: %d", n)
	}
	if n := ruleCount("itest-af1", ""); n != 2 {
		t.Fatalf("unfiltered rules: %d", n)
	}
	if got := ids("itest-af1", ""); len(got) == 0 {
		t.Fatal("unfiltered list empty")
	}
}

// Exact subtree device count: the dashboard asset filter uses it to tell a
// complete device list from one cut by the 1000-row list limit.
func TestIntegrationAssetDeviceCount(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-ac1")
	seed(t, s, "itest-ac2")
	ctx := t.Context()
	clean := func() {
		for _, tn := range []string{"itest-ac1", "itest-ac2"} {
			s.st.Pool.Exec(ctx, `UPDATE devices SET asset_id=NULL WHERE tenant_id=$1`, tn)
			s.st.Pool.Exec(ctx, `DELETE FROM assets WHERE tenant_id=$1 AND parent_id IS NOT NULL`, tn)
			s.st.Pool.Exec(ctx, `DELETE FROM assets WHERE tenant_id=$1`, tn)
			s.st.Pool.Exec(ctx, `DELETE FROM devices WHERE tenant_id=$1 AND id LIKE 'itest-ac1-d%'`, tn)
		}
	}
	clean()
	t.Cleanup(clean)
	for _, q := range []string{
		`INSERT INTO assets(id,tenant_id,parent_id,name,kind) VALUES('ac-plant','itest-ac1',NULL,'P','plant'),('ac-other','itest-ac2',NULL,'O','plant')`,
		`INSERT INTO assets(id,tenant_id,parent_id,name,kind) VALUES('ac-line','itest-ac1','ac-plant','L','line')`,
		`UPDATE devices SET asset_id='ac-line' WHERE id='itest-ac1-dev'`,
		`INSERT INTO devices(id,tenant_id,gateway_id,profile,name,asset_id) VALUES('itest-ac1-d2','itest-ac1','itest-ac1-gw','modbus-tcp','second','ac-plant')`,
	} {
		if _, err := s.st.Pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/assets/{id}/device-count", s.getAssetDeviceCount)
	count := func(tenant, asset string) (int, int) {
		w := call(api, tenant, "viewer", "GET", "/v1/assets/"+asset+"/device-count", "")
		var o map[string]any
		json.Unmarshal(w.Body.Bytes(), &o)
		n, _ := o["count"].(float64)
		return w.Code, int(n)
	}
	if code, n := count("itest-ac1", "ac-plant"); code != 200 || n != 2 {
		t.Fatalf("subtree count = %d (code %d), want 2 (device on child + device on root)", n, code)
	}
	if code, n := count("itest-ac1", "ac-line"); code != 200 || n != 1 {
		t.Fatalf("child count = %d (code %d), want 1", n, code)
	}
	if code, n := count("itest-ac1", "ac-other"); code != 200 || n != 0 {
		t.Fatalf("other tenant's asset = %d (code %d), want 0", n, code)
	}
	if code, n := count("itest-ac1", "ac-ghost"); code != 200 || n != 0 {
		t.Fatalf("unknown asset = %d (code %d), want 0", n, code)
	}
}

// A rule scoped by asset_id plus relation covers devices attached to assets
// the anchor reaches downstream via that relation (cycle-safe, depth 10) and
// devices named directly as endpoints - never the anchor's own devices.
func TestIntegrationRelationScopedRule(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-rs1")
	seed(t, s, "itest-rs2")
	ctx := context.Background()
	pool := s.st.Pool
	clean := func() {
		for _, q := range []string{
			`DELETE FROM alerts WHERE tenant_id IN ('itest-rs1','itest-rs2')`,
			`DELETE FROM rules WHERE tenant_id IN ('itest-rs1','itest-rs2')`,
			`DELETE FROM entity_relations WHERE tenant_id IN ('itest-rs1','itest-rs2')`,
			`UPDATE devices SET asset_id=NULL WHERE tenant_id IN ('itest-rs1','itest-rs2')`,
			`DELETE FROM devices WHERE tenant_id IN ('itest-rs1','itest-rs2') AND id LIKE 'itest-rs%-d%'`,
			`DELETE FROM assets WHERE tenant_id IN ('itest-rs1','itest-rs2')`,
		} {
			pool.Exec(ctx, q)
		}
	}
	clean()
	t.Cleanup(clean)
	for _, q := range []string{
		`INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('rs-user','itest-rs1','rs@x.local','rs','admin') ON CONFLICT DO NOTHING`,
		`INSERT INTO assets(id,tenant_id,parent_id,name,kind) VALUES
		 ('rs-line','itest-rs1',NULL,'Line','line'),
		 ('rs-motor1','itest-rs1',NULL,'Motor one','machine'),
		 ('rs-motor2','itest-rs1',NULL,'Motor two','machine')`,
		// seed device -> motor1; d2 -> motor2; d3 -> the anchor itself; d4 unattached
		`UPDATE devices SET asset_id='rs-motor1' WHERE id='itest-rs1-dev'`,
		`INSERT INTO devices(id,tenant_id,gateway_id,profile,name,asset_id) VALUES
		 ('itest-rs1-d2','itest-rs1','itest-rs1-gw','modbus-tcp','second','rs-motor2'),
		 ('itest-rs1-d3','itest-rs1','itest-rs1-gw','modbus-tcp','anchor own','rs-line'),
		 ('itest-rs1-d4','itest-rs1','itest-rs1-gw','modbus-tcp','loose',NULL)`,
		// line feeds motor1 feeds motor2 (and a cycle back), line powers d4 directly
		`INSERT INTO entity_relations(tenant_id,from_kind,from_id,relation,to_kind,to_id) VALUES
		 ('itest-rs1','asset','rs-line','feeds','asset','rs-motor1'),
		 ('itest-rs1','asset','rs-motor1','feeds','asset','rs-motor2'),
		 ('itest-rs1','asset','rs-motor2','feeds','asset','rs-line'),
		 ('itest-rs1','asset','rs-line','powers','device','itest-rs1-d4')`,
		`INSERT INTO rules(id,tenant_id,name,definition,enabled,created_by) VALUES
		 ('rs-feeds','itest-rs1','fed motors','{"point_id":"temp","op":">","threshold":1,"severity":"info","asset_id":"rs-line","relation":"feeds"}',true,'rs-user'),
		 ('rs-powers','itest-rs1','powered','{"point_id":"temp","op":">","threshold":1,"severity":"info","asset_id":"rs-line","relation":"powers"}',true,'rs-user')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	fired := func(rule, device string) (n int) {
		pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE rule_id=$1 AND device_id=$2`, rule, device).Scan(&n)
		return
	}
	for _, dev := range []string{"itest-rs1-dev", "itest-rs1-d2", "itest-rs1-d3", "itest-rs1-d4"} {
		rules.Evaluate(ctx, pool, nil, "itest-rs1", dev, "temp", 5)
	}
	if fired("rs-feeds", "itest-rs1-dev") != 1 || fired("rs-feeds", "itest-rs1-d2") != 1 {
		t.Fatal("feeds rule must fire for devices on reached assets (depth 1 and 2)")
	}
	if fired("rs-feeds", "itest-rs1-d3") != 0 {
		t.Fatal("feeds rule must not fire for the anchor asset's own device")
	}
	if fired("rs-feeds", "itest-rs1-d4") != 0 || fired("rs-powers", "itest-rs1-d2") != 0 {
		t.Fatal("rule must follow only its own relation")
	}
	if fired("rs-powers", "itest-rs1-d4") != 1 {
		t.Fatal("powers rule must fire for a device named directly as an endpoint")
	}
	// cross-tenant: same point reading on the other tenant fires nothing
	rules.Evaluate(ctx, pool, nil, "itest-rs2", "itest-rs2-dev", "temp", 5)
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE tenant_id='itest-rs2'`).Scan(&n)
	if n != 0 {
		t.Fatal("cross-tenant evaluation")
	}
	// API validation: relation without asset_id, and a bad relation name
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/rules", s.createRule)
	if w := call(api, "itest-rs1", "admin", "POST", "/v1/rules", `{"name":"x","enabled":true,"definition":{"point_id":"temp","op":">","threshold":1,"severity":"info","relation":"feeds"}}`); w.Code != 400 {
		t.Fatalf("relation without asset_id = %d, want 400", w.Code)
	}
	if w := call(api, "itest-rs1", "admin", "POST", "/v1/rules", `{"name":"x","enabled":true,"definition":{"point_id":"temp","op":">","threshold":1,"severity":"info","asset_id":"rs-line","relation":"Feeds"}}`); w.Code != 400 {
		t.Fatalf("bad relation name = %d, want 400", w.Code)
	}
	if w := call(api, "itest-rs1", "admin", "POST", "/v1/rules", `{"name":"x","enabled":true,"definition":{"point_id":"temp","op":">","threshold":1,"severity":"info","asset_id":"rs-line","relation":"feeds"}}`); w.Code != 201 {
		t.Fatalf("valid relation rule = %d, want 201", w.Code)
	}
}

// ?asset_id plus ?relation on the device list and the device-count endpoint:
// the same relation walk the rules use, so the dashboard filter can cover
// what an asset reaches via a typed relation instead of its subtree.
func TestIntegrationRelationDeviceQuery(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-rq1")
	seed(t, s, "itest-rq2")
	ctx := context.Background()
	pool := s.st.Pool
	clean := func() {
		for _, q := range []string{
			`DELETE FROM entity_relations WHERE tenant_id IN ('itest-rq1','itest-rq2')`,
			`UPDATE devices SET asset_id=NULL WHERE tenant_id IN ('itest-rq1','itest-rq2')`,
			`DELETE FROM devices WHERE tenant_id IN ('itest-rq1','itest-rq2') AND id LIKE 'itest-rq%-d%'`,
			`DELETE FROM assets WHERE tenant_id IN ('itest-rq1','itest-rq2')`,
		} {
			pool.Exec(ctx, q)
		}
	}
	clean()
	t.Cleanup(clean)
	for _, q := range []string{
		`INSERT INTO assets(id,tenant_id,parent_id,name,kind) VALUES
		 ('rq-line','itest-rq1',NULL,'Line','line'),
		 ('rq-motor1','itest-rq1',NULL,'Motor one','machine'),
		 ('rq-motor2','itest-rq1',NULL,'Motor two','machine')`,
		`UPDATE devices SET asset_id='rq-motor1' WHERE id='itest-rq1-dev'`,
		`INSERT INTO devices(id,tenant_id,gateway_id,profile,name,asset_id) VALUES
		 ('itest-rq1-d2','itest-rq1','itest-rq1-gw','modbus-tcp','second','rq-motor2'),
		 ('itest-rq1-d3','itest-rq1','itest-rq1-gw','modbus-tcp','anchor own','rq-line'),
		 ('itest-rq1-d4','itest-rq1','itest-rq1-gw','modbus-tcp','loose',NULL)`,
		`INSERT INTO entity_relations(tenant_id,from_kind,from_id,relation,to_kind,to_id) VALUES
		 ('itest-rq1','asset','rq-line','feeds','asset','rq-motor1'),
		 ('itest-rq1','asset','rq-motor1','feeds','asset','rq-motor2'),
		 ('itest-rq1','asset','rq-motor2','feeds','asset','rq-line'),
		 ('itest-rq1','asset','rq-line','powers','device','itest-rq1-d4')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/devices", s.listDevices)
	api.HandleFunc("GET /v1/assets/{id}/device-count", s.getAssetDeviceCount)
	ids := func(tenant, q string) []string {
		w := call(api, tenant, "viewer", "GET", "/v1/devices"+q, "")
		var l []map[string]any
		json.Unmarshal(w.Body.Bytes(), &l)
		out := []string{}
		for _, d := range l {
			out = append(out, d["id"].(string))
		}
		return out
	}
	count := func(tenant, asset, q string) int {
		w := call(api, tenant, "viewer", "GET", "/v1/assets/"+asset+"/device-count"+q, "")
		var o map[string]any
		json.Unmarshal(w.Body.Bytes(), &o)
		n, _ := o["count"].(float64)
		return int(n)
	}
	// subtree (no relation): the anchor's own device, nothing else
	if got := ids("itest-rq1", "?asset_id=rq-line"); len(got) != 1 || got[0] != "itest-rq1-d3" {
		t.Fatalf("subtree: %v", got)
	}
	// feeds walk: motor1 + motor2 devices (cycle back to the anchor must not re-add d3)
	got := ids("itest-rq1", "?asset_id=rq-line&relation=feeds")
	if len(got) != 2 {
		t.Fatalf("feeds devices: %v", got)
	}
	if count("itest-rq1", "rq-line", "?relation=feeds") != 2 || count("itest-rq1", "rq-line", "") != 1 {
		t.Fatal("feeds count = 2, subtree count = 1")
	}
	// powers walk: the directly related device, no assets
	if got := ids("itest-rq1", "?asset_id=rq-line&relation=powers"); len(got) != 1 || got[0] != "itest-rq1-d4" {
		t.Fatalf("powers devices: %v", got)
	}
	if count("itest-rq1", "rq-line", "?relation=powers") != 1 {
		t.Fatal("powers count = 1")
	}
	// validation: relation without asset, bad name; cross-tenant empty
	if w := call(api, "itest-rq1", "viewer", "GET", "/v1/devices?relation=feeds", ""); w.Code != 400 {
		t.Fatalf("relation without asset_id = %d, want 400", w.Code)
	}
	if w := call(api, "itest-rq1", "viewer", "GET", "/v1/devices?asset_id=rq-line&relation=Feeds", ""); w.Code != 400 {
		t.Fatalf("bad relation name = %d, want 400", w.Code)
	}
	if w := call(api, "itest-rq1", "viewer", "GET", "/v1/assets/rq-line/device-count?relation=Bad", ""); w.Code != 400 {
		t.Fatalf("count bad relation = %d, want 400", w.Code)
	}
	if got := ids("itest-rq2", "?asset_id=rq-line&relation=feeds"); len(got) != 0 {
		t.Fatalf("cross-tenant: %v", got)
	}
	// alerts follow the same walk, so a relation-driven dashboard never mixes scopes
	pool.Exec(ctx, `DELETE FROM alerts WHERE tenant_id='itest-rq1' AND id LIKE 'rq-al%'`)
	pool.Exec(ctx, `INSERT INTO alerts(id,tenant_id,severity,message,device_id) VALUES
	  ('rq-al1','itest-rq1','warning','boiler','itest-rq1-d2'),
	  ('rq-al2','itest-rq1','warning','anchor','itest-rq1-d3'),
	  ('rq-al3','itest-rq1','warning','loose','itest-rq1-d4')`)
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM alerts WHERE tenant_id='itest-rq1' AND id LIKE 'rq-al%'`) })
	api.HandleFunc("GET /v1/alerts", s.listAlerts)
	alertIDs := func(q string) int {
		w := call(api, "itest-rq1", "viewer", "GET", "/v1/alerts"+q, "")
		var l []map[string]any
		json.Unmarshal(w.Body.Bytes(), &l)
		return len(l)
	}
	if n := alertIDs("?asset_id=rq-line&relation=feeds"); n != 1 {
		t.Fatalf("feeds alerts = %d, want 1 (motor2 only)", n)
	}
	if n := alertIDs("?asset_id=rq-line&relation=powers"); n != 1 {
		t.Fatalf("powers alerts = %d, want 1 (direct device)", n)
	}
	if n := alertIDs("?asset_id=rq-line"); n != 1 {
		t.Fatalf("subtree alerts = %d, want 1 (anchor)", n)
	}
	if w := call(api, "itest-rq1", "viewer", "GET", "/v1/alerts?relation=feeds", ""); w.Code != 400 {
		t.Fatalf("alerts relation without asset_id = %d, want 400", w.Code)
	}
}
