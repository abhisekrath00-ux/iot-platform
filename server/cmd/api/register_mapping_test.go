package main

import (
	"encoding/json"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/commission"
)

func TestIntegrationRegisterRangeRequestFencing(t *testing.T) {
	s, _ := testServer(t)
	ctx := t.Context()
	const tenant = "itest-register-map"
	seed(t, s, tenant)
	const id = "itest-register-map-session"
	if _, err := s.st.Pool.Exec(ctx, `DELETE FROM commissioning_sessions WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	_, err := s.st.Pool.Exec(ctx, `INSERT INTO commissioning_sessions(id,tenant_id,site_id,gateway_id,serial,state,created_by,port_test) VALUES($1,$2,$3,$4,'mapping-fixture','claimed','test-user','{"config":{"request_id":"new-request"}}')`, id, tenant, tenant+"-site", tenant+"-gw")
	if err != nil {
		t.Fatal(err)
	}
	res := commission.ProbeResult{SessionID: id, RequestID: "old-request", OK: true, Readings: json.RawMessage(`[{"point_id":"r10","value":100}]`)}
	for _, test := range []struct {
		tenant, gateway, request string
		want                     int64
	}{{tenant, tenant + "-gw", "old-request", 0}, {tenant, tenant + "-gw", "", 0}, {"foreign", tenant + "-gw", "new-request", 0}, {tenant, "foreign", "new-request", 0}, {tenant, tenant + "-gw", "new-request", 1}} {
		res.RequestID = test.request
		n, err := commission.RecordResult(ctx, s.st.Pool, test.tenant, test.gateway, res)
		if err != nil || n != test.want {
			t.Fatalf("fence %+v n%d err%v", test, n, err)
		}
	}
	var got string
	if err = s.st.Pool.QueryRow(ctx, `SELECT port_test->'result'->>'request_id' FROM commissioning_sessions WHERE id=$1`, id).Scan(&got); err != nil || got != "new-request" {
		t.Fatal(got, err)
	}
	// A late older result cannot overwrite the accepted exact response.
	res.RequestID = "old-request"
	res.OK = false
	if n, err := commission.RecordResult(ctx, s.st.Pool, tenant, tenant+"-gw", res); err != nil || n != 0 {
		t.Fatal("late overwrite", n, err)
	}
	if _, err = s.st.Pool.Exec(ctx, `UPDATE commissioning_sessions SET port_test='{"config":{}}' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	res.RequestID = ""
	if n, err := commission.RecordResult(ctx, s.st.Pool, tenant, tenant+"-gw", res); err != nil || n != 1 {
		t.Fatal("legacy request compatibility", n, err)
	}
}
