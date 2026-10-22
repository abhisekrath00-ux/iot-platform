package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// GET /v1/alerts?group_id= returns only alerts on devices that are members of
// the group; combined with asset/status filters it intersects, and an unknown
// or deleted group matches nothing rather than everything.
func TestIntegrationGroupScopedAlerts(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-ag1")
	ctx := context.Background()
	pool := s.st.Pool
	clean := func() {
		for _, q := range []string{
			`DELETE FROM alerts WHERE tenant_id='itest-ag1'`,
			`DELETE FROM device_group_members WHERE tenant_id='itest-ag1'`,
			`DELETE FROM device_groups WHERE tenant_id='itest-ag1'`,
			`DELETE FROM devices WHERE tenant_id='itest-ag1' AND id LIKE 'itest-ag1-x%'`,
		} {
			pool.Exec(ctx, q)
		}
	}
	clean()
	t.Cleanup(clean)
	for _, q := range []string{
		`INSERT INTO device_groups(id,tenant_id,name) VALUES ('ag-g1','itest-ag1','Compressors')`,
		`INSERT INTO devices(id,tenant_id,gateway_id,profile,name) VALUES
		 ('itest-ag1-x1','itest-ag1','itest-ag1-gw','modbus-tcp','Compressor 1'),
		 ('itest-ag1-x2','itest-ag1','itest-ag1-gw','modbus-tcp','Loose sensor')`,
		`INSERT INTO device_group_members(group_id,tenant_id,device_id) VALUES ('ag-g1','itest-ag1','itest-ag1-x1')`,
		`INSERT INTO alerts(id,tenant_id,device_id,severity,message,status) VALUES
		 ('ag-a1','itest-ag1','itest-ag1-x1','warning','member high temp','open'),
		 ('ag-a2','itest-ag1','itest-ag1-x2','critical','non-member high temp','open'),
		 ('ag-a3','itest-ag1','itest-ag1-x1','info','member resolved note','resolved')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/alerts", s.listAlerts)
	ids := func(path string) []string {
		w := call(mux, "itest-ag1", "admin", "GET", path, "")
		if w.Code != 200 {
			t.Fatalf("%s = %d, want 200", path, w.Code)
		}
		var rows []map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, r := range rows {
			out = append(out, r["id"].(string))
		}
		return out
	}
	got := ids("/v1/alerts?status=open&group_id=ag-g1")
	if len(got) != 1 || got[0] != "ag-a1" {
		t.Fatalf("group filter = %v, want only the member device's open alert", got)
	}
	if got := ids("/v1/alerts?group_id=ag-g1"); len(got) != 2 {
		t.Fatalf("group filter without status = %v, want both member alerts", got)
	}
	if got := ids("/v1/alerts?group_id=no-such-group"); len(got) != 0 {
		t.Fatalf("unknown group = %v, want no alerts", got)
	}
	// a device removed from the group drops out of the filter
	pool.Exec(ctx, `DELETE FROM device_group_members WHERE tenant_id='itest-ag1' AND device_id='itest-ag1-x1'`)
	if got := ids("/v1/alerts?group_id=ag-g1"); len(got) != 0 {
		t.Fatalf("after membership removal = %v, want no alerts", got)
	}
}
