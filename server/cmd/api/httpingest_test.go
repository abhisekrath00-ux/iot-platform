package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestIntegrationHTTPIngest(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-hi1")
	seed(t, s, "itest-hi2")
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/telemetry/ingest", s.ingestHTTP)
	post := func(tenant, role, body string) (int, map[string]any) {
		w := call(api, tenant, role, "POST", "/v1/telemetry/ingest", body)
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	dev := `"device_id":"itest-hi1-dev"`
	// good, out of range, unknown point, NaN-ish (null value)
	code, out := post("itest-hi1", "operator", `{`+dev+`,"readings":[{"point":"temp","value":42.5,"event_id":"hi-1"},{"point":"temp","value":999},{"point":"nope","value":1},{"point":"temp"}]}`)
	if code != 200 || out["accepted"].(float64) != 1 || len(out["rejected"].([]any)) != 3 {
		t.Fatalf("mixed batch: %d %v", code, out)
	}
	// retry with same event id is idempotent (accepted count says inserted attempt, row count stays 1)
	post("itest-hi1", "operator", `{`+dev+`,"readings":[{"point":"temp","value":42.5,"event_id":"hi-1"}]}`)
	var n int
	s.st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM telemetry WHERE tenant_id='itest-hi1' AND event_id='hi-1'`).Scan(&n)
	if n != 1 {
		t.Fatalf("duplicate event stored %d times", n)
	}
	// viewer cannot push
	if code, _ := post("itest-hi1", "viewer", `{`+dev+`,"readings":[{"point":"temp","value":1}]}`); code != 403 {
		t.Fatalf("viewer got %d", code)
	}
	// another tenant cannot write to this device
	if code, _ := post("itest-hi2", "operator", `{`+dev+`,"readings":[{"point":"temp","value":1}]}`); code != 404 {
		t.Fatalf("cross-tenant got %d", code)
	}
	// implausible timestamp and oversize batch
	if code, _ := post("itest-hi1", "operator", `{`+dev+`,"readings":[{"point":"temp","value":1,"ts":"2001-01-01T00:00:00Z"}]}`); code != 422 {
		t.Fatalf("old ts got %d", code)
	}
	big := strings.Repeat(`{"point":"temp","value":1},`, maxIngestBatch+1)
	if code, _ := post("itest-hi1", "operator", fmt.Sprintf(`{%s,"readings":[%s]}`, dev, strings.TrimSuffix(big, ","))); code != 413 {
		t.Fatalf("oversize got %d", code)
	}
}
