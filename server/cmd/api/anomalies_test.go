package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestIntegrationAnomaliesEndpoint(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-an1")
	seed(t, s, "itest-an2")
	ctx := t.Context()
	// 40 steady readings plus one spike, 1 minute apart.
	if _, err := s.st.Pool.Exec(ctx, `INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,value,unit,quality,schema_version)
	  SELECT 'an-'||g,'itest-an1','itest-an1-gw','itest-an1-dev','temp', now()-(g||' minutes')::interval, CASE WHEN g=107 THEN 140 ELSE 70+(g%5)*0.2 END,'C','measured',1
	  FROM generate_series(100,140) g`); err != nil {
		t.Fatal(err)
	}
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/telemetry/anomalies", s.anomaliesTelemetry)
	get := func(tenant, qs string) (int, map[string]any) {
		w := call(api, tenant, "viewer", "GET", "/v1/telemetry/anomalies?"+qs, "")
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	code, out := get("itest-an1", "device_id=itest-an1-dev&point_id=temp&hours=24")
	if code != 200 || out["enough_data"] != true {
		t.Fatalf("%d %v", code, out)
	}
	a := out["anomalies"].([]any)
	found := false
	for _, x := range a {
		if x.(map[string]any)["value"].(float64) == 140 {
			found = true
		}
	}
	if !found {
		t.Fatalf("the 140 spike was not flagged: %v", out)
	}
	if code, out := get("itest-an2", "device_id=itest-an1-dev&point_id=temp"); code != 200 || out["samples"].(float64) != 0 || out["enough_data"] != false {
		t.Fatalf("cross-tenant must see no samples: %v", out)
	}
	if code, _ := get("itest-an1", "device_id=itest-an1-dev&point_id=temp&threshold=1"); code != 400 {
		t.Fatalf("bad threshold %d", code)
	}
	if code, _ := get("itest-an1", "point_id=temp"); code != 400 {
		t.Fatalf("missing device %d", code)
	}
}
