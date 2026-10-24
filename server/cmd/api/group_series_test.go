package main

import (
	"encoding/json"
	"math"
	"net/http"
	"testing"
)

func TestIntegrationGroupSeries(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-gseries")
	p := s.st.Pool
	ctx := t.Context()
	cleanup := func() {
		for _, q := range []string{`DELETE FROM telemetry WHERE tenant_id='itest-gseries'`, `DELETE FROM device_group_members WHERE tenant_id='itest-gseries'`, `DELETE FROM device_groups WHERE tenant_id='itest-gseries'`} {
			p.Exec(ctx, q)
		}
	}
	cleanup()
	t.Cleanup(cleanup)
	for _, q := range []string{
		`INSERT INTO device_groups(id,tenant_id,name,parent_id) VALUES('gs-root','itest-gseries','Root',NULL),('gs-child','itest-gseries','Child','gs-root')`,
		`INSERT INTO devices(id,tenant_id,gateway_id,profile,name) VALUES('gs-second','itest-gseries','itest-gseries-gw','meter','Second'),('gs-out','itest-gseries','itest-gseries-gw','meter','Outside')`,
		`INSERT INTO device_group_members(group_id,tenant_id,device_id) VALUES('gs-root','itest-gseries','itest-gseries-dev'),('gs-child','itest-gseries','gs-second')`,
		// One device has two readings: device means 15 and 30, sum 45, not
		// raw-sample avg 20 or raw-sample sum 60. Outside 999 never counts.
		`INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,value,unit,schema_version) VALUES
 ('gs1','itest-gseries','itest-gseries-gw','itest-gseries-dev','power',date_trunc('hour',now())-interval '2 hours',10,'kW',1),
 ('gs2','itest-gseries','itest-gseries-gw','itest-gseries-dev','power',date_trunc('hour',now())-interval '2 hours'+interval '1 minute',20,'kW',1),
 ('gs3','itest-gseries','itest-gseries-gw','gs-second','power',date_trunc('hour',now())-interval '2 hours',30,'kW',1),
 ('gs4','itest-gseries','itest-gseries-gw','gs-out','power',date_trunc('hour',now())-interval '2 hours',999,'kW',1),
 ('gs5','itest-gseries','itest-gseries-gw','gs-second','power',date_trunc('hour',now())-interval '1 hour',40,'kW',1)`} {
		if _, err := p.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/telemetry/group-series", s.groupSeriesTelemetry)
	type row struct {
		V                         *float64
		Devices, Members, Samples int
		Unit                      string
		Mixed                     bool `json:"units_mixed"`
	}
	get := func(agg, group string) []row {
		t.Helper()
		w := call(mux, "itest-gseries", "viewer", "GET", "/v1/telemetry/group-series?group_id="+group+"&point_id=power&agg="+agg, "")
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		var r []row
		json.Unmarshal(w.Body.Bytes(), &r)
		return r
	}
	for agg, want := range map[string]float64{"sum": 45, "avg": 22.5, "min": 15, "max": 30} {
		r := get(agg, "gs-root")
		if len(r) != 2 || r[0].V == nil || math.Abs(*r[0].V-want) > 1e-8 || r[0].Devices != 2 || r[0].Samples != 3 || r[0].Unit != "kW" || r[1].Devices != 1 || r[1].Members != 2 {
			t.Fatalf("%s %+v", agg, r)
		}
	}
	if r := get("sum", "gs-child"); *r[0].V != 30 {
		t.Fatal(r)
	}
	// Invalid quality values are placeholders, not observations. Nonfinite
	// values are excluded before both averaging and the unit check.
	for _, q := range []string{
		`INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,value,unit,quality,schema_version) VALUES
 ('gs-stale','itest-gseries','itest-gseries-gw','itest-gseries-dev','power',date_trunc('hour',now())-interval '2 hours',999,'W','stale',1),
 ('gs-missing','itest-gseries','itest-gseries-gw','gs-second','power',date_trunc('hour',now())-interval '2 hours',999,'A','missing',1),
 ('gs-nan','itest-gseries','itest-gseries-gw','gs-second','power',date_trunc('hour',now())-interval '2 hours','NaN'::float8,'A','measured',1),
 ('gs-inf','itest-gseries','itest-gseries-gw','gs-second','power',date_trunc('hour',now())-interval '2 hours','Infinity'::float8,'A','estimated',1),
 ('gs-neginf','itest-gseries','itest-gseries-gw','gs-second','power',date_trunc('hour',now())-interval '2 hours','-Infinity'::float8,'A','measured',1),
 ('gs-invalid-hour','itest-gseries','itest-gseries-gw','gs-second','power',date_trunc('hour',now())-interval '3 hours',999,'A','missing',1)`} {
		if _, err := p.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if r := get("sum", "gs-root"); len(r) != 2 || r[0].V == nil || *r[0].V != 45 || r[0].Samples != 3 || r[0].Mixed || r[0].Unit != "kW" {
		t.Fatalf("invalid raw contributed: %+v", r)
	}
	// Estimated finite samples remain useful observations and count honestly.
	if _, err := p.Exec(ctx, `UPDATE telemetry SET quality='estimated' WHERE event_id='gs2'`); err != nil {
		t.Fatal(err)
	}
	if r := get("avg", "gs-root"); r[0].V == nil || *r[0].V != 22.5 || r[0].Samples != 3 {
		t.Fatalf("estimate excluded: %+v", r)
	}
	// A unit change anywhere in the window invalidates the entire series,
	// rather than connecting incompatible units under one axis label.
	p.Exec(ctx, `UPDATE telemetry SET unit='W' WHERE event_id='gs5'`)
	for _, r := range get("sum", "gs-root") {
		if !r.Mixed || r.V != nil || r.Unit != "" {
			t.Fatal("mixed units plotted", r)
		}
	}
	p.Exec(ctx, `UPDATE telemetry SET unit='kW' WHERE event_id='gs5'`)
	// Scope middleware and SQL both enforce a customer subtree.
	p.Exec(ctx, `INSERT INTO customers(id,tenant_id,name) VALUES('gs-c','itest-gseries','Customer')`)
	p.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('gs-view','itest-gseries','gs@example.invalid','Scoped','viewer')`)
	p.Exec(ctx, `INSERT INTO user_customer_scope(tenant_id,user_id,customer_id) VALUES('itest-gseries','gs-view','gs-c')`)
	p.Exec(ctx, `UPDATE devices SET customer_id='gs-c' WHERE id='gs-second'`)
	w := callAs(s.customerScope(mux), "itest-gseries", "gs-view", "viewer", "GET", "/v1/telemetry/group-series?group_id=gs-root&point_id=power&agg=sum", "")
	var scoped []row
	json.Unmarshal(w.Body.Bytes(), &scoped)
	if w.Code != 200 || len(scoped) != 2 || scoped[0].V == nil || *scoped[0].V != 30 || scoped[0].Members != 1 {
		t.Fatalf("scope %d %s", w.Code, w.Body)
	}
	for _, path := range []string{"?group_id=gs-root&agg=sum", "?group_id=gs-root&point_id=power&agg=bad"} {
		if call(mux, "itest-gseries", "viewer", "GET", "/v1/telemetry/group-series"+path, "").Code != 400 {
			t.Fatal("bad input")
		}
	}
	if call(mux, "another-tenant", "viewer", "GET", "/v1/telemetry/group-series?group_id=gs-root&point_id=power&agg=sum", "").Code != 404 {
		t.Fatal("cross tenant")
	}
}
