package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestParseBulkCSVCollectsEveryProblem(t *testing.T) {
	_, p := parseBulkCSV(strings.NewReader("gateway_id,name\ng1,x\n"))
	if len(p) == 0 || !strings.Contains(p[0], "profile_id") {
		t.Fatalf("missing column not reported: %v", p)
	}
	rows, p := parseBulkCSV(strings.NewReader("\ufeffgateway_id,profile_id,name,tags\ng1,p1,Meter A,floor-1;energy\n,p1,Bad row,\ng1,p1,Tagged,BAD TAG!\ng1,p1,Ok,\n"))
	if len(rows) != 2 || len(p) != 2 {
		t.Fatalf("rows=%d problems=%v", len(rows), p)
	}
	if !strings.HasPrefix(p[0], "line 3:") || !strings.HasPrefix(p[1], "line 4:") {
		t.Fatalf("line numbers wrong: %v", p)
	}
	if len(rows[0].Tags) != 2 || rows[0].Tags[0] != "energy" {
		t.Fatalf("tags %v", rows[0].Tags)
	}
	if _, p := parseBulkCSV(strings.NewReader("gateway_id,profile_id,name\n")); len(p) != 1 || p[0] != "no data rows" {
		t.Fatalf("empty file: %v", p)
	}
}

func TestIntegrationBulkDeviceCreate(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-bulk")
	seed(t, s, "itest-bulk2")
	ctx := t.Context()
	s.st.Pool.Exec(ctx, `DELETE FROM device_profiles WHERE tenant_id IN ('itest-bulk','itest-bulk2')`)
	s.st.Pool.Exec(ctx, `INSERT INTO device_profiles(id,tenant_id,name,driver_profile,points,created_by) VALUES('bulk-prof','itest-bulk','Meter','modbus-generic','[{"id":"kw","unit":"kW","min":0,"max":500},{"id":"kwh","unit":"kWh","min":0,"max":1000000}]','test-user')`)
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/devices/bulk", s.bulkCreateDevices)
	csv := "gateway_id,profile_id,name,tags\nitest-bulk-gw,bulk-prof,Bulk A,line-1\nitest-bulk-gw,bulk-prof,Bulk B,\n"
	count := func() int {
		var n int
		s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM devices WHERE tenant_id='itest-bulk' AND name LIKE 'Bulk %'`).Scan(&n)
		return n
	}
	if w := call(api, "itest-bulk", "viewer", "POST", "/v1/devices/bulk", csv); w.Code != 403 {
		t.Fatalf("viewer = %d, want 403", w.Code)
	}
	if w := call(api, "itest-bulk", "admin", "POST", "/v1/devices/bulk?dry_run=1", csv); w.Code != 200 || !strings.Contains(w.Body.String(), `"would_create":2`) || count() != 0 {
		t.Fatalf("dry run %d %s created=%d", w.Code, w.Body.String(), count())
	}
	// one bad row rejects the whole file; nothing is created
	bad := csv + "itest-bulk2-gw,bulk-prof,Foreign gateway,\nitest-bulk-gw,nope,Unknown profile,\n"
	w := call(api, "itest-bulk", "admin", "POST", "/v1/devices/bulk", bad)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "unknown gateway") || !strings.Contains(w.Body.String(), "unknown profile_id") || count() != 0 {
		t.Fatalf("bad file %d %s created=%d", w.Code, w.Body.String(), count())
	}
	w = call(api, "itest-bulk", "installer", "POST", "/v1/devices/bulk", csv)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"created":2`) || count() != 2 {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var pts int
	s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM points p JOIN devices d ON d.id=p.device_id WHERE d.tenant_id='itest-bulk' AND d.name LIKE 'Bulk %'`).Scan(&pts)
	if pts != 4 {
		t.Fatalf("points from profile template = %d, want 4", pts)
	}
	// another tenant cannot use this tenant's profile
	if w := call(api, "itest-bulk2", "admin", "POST", "/v1/devices/bulk", "gateway_id,profile_id,name\nitest-bulk2-gw,bulk-prof,Steal,\n"); w.Code != 400 {
		t.Fatalf("cross-tenant profile = %d", w.Code)
	}
	if w := call(api, "itest-bulk", "admin", "POST", "/v1/devices/bulk", "gateway_id,profile_id,name\n"+strings.Repeat("itest-bulk-gw,bulk-prof,X,\n", 501)); w.Code != 400 || !strings.Contains(w.Body.String(), "more than 500") {
		t.Fatalf("row cap = %d", w.Code)
	}
}
