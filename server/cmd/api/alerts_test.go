package main

import (
	"github.com/abhisekrath00-ux/iot-platform/server/internal/report"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestIntegrationAlertLifecycle(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-al1")
	seed(t, s, "itest-al2")
	for _, q := range []string{
		`INSERT INTO alerts(id,tenant_id,severity,message) VALUES('itest-al1-a','itest-al1','critical','hot') ON CONFLICT (id) DO UPDATE SET status='open', acknowledged_by=NULL, acknowledged_at=NULL, resolved_by=NULL, resolved_at=NULL`,
	} {
		if _, err := s.st.Pool.Exec(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/alerts", s.listAlerts)
	api.HandleFunc("GET /v1/alerts/{id}", s.getAlert)
	api.HandleFunc("POST /v1/alerts/{id}/ack", s.ackAlert)
	api.HandleFunc("POST /v1/alerts/{id}/resolve", s.resolveAlert)
	api.HandleFunc("POST /v1/alerts/{id}/comments", s.commentAlert)

	// viewers cannot act
	if w := call(api, "itest-al1", "viewer", "POST", "/v1/alerts/itest-al1-a/ack", ""); w.Code != 403 {
		t.Fatalf("viewer ack = %d, want 403", w.Code)
	}
	// another tenant cannot touch it
	if w := call(api, "itest-al2", "admin", "POST", "/v1/alerts/itest-al1-a/ack", ""); w.Code != 409 {
		t.Fatalf("cross-tenant ack = %d, want 409", w.Code)
	}
	if w := call(api, "itest-al2", "admin", "POST", "/v1/alerts/itest-al1-a/comments", `{"body":"x"}`); w.Code != 404 {
		t.Fatalf("cross-tenant comment = %d, want 404", w.Code)
	}
	if w := call(api, "itest-al2", "admin", "GET", "/v1/alerts/itest-al1-a", ""); w.Code != 404 {
		t.Fatalf("cross-tenant get = %d, want 404", w.Code)
	}
	// open -> ack ok, second ack conflicts
	if w := call(api, "itest-al1", "operator", "POST", "/v1/alerts/itest-al1-a/ack", ""); w.Code != 200 {
		t.Fatalf("ack = %d %s", w.Code, w.Body.String())
	}
	if w := call(api, "itest-al1", "operator", "POST", "/v1/alerts/itest-al1-a/ack", ""); w.Code != 409 {
		t.Fatalf("double ack = %d, want 409", w.Code)
	}
	// comments validated
	if w := call(api, "itest-al1", "operator", "POST", "/v1/alerts/itest-al1-a/comments", `{"body":"  "}`); w.Code != 400 {
		t.Fatalf("empty comment = %d, want 400", w.Code)
	}
	if w := call(api, "itest-al1", "operator", "POST", "/v1/alerts/itest-al1-a/comments", `{"body":"checking the boiler"}`); w.Code != 201 {
		t.Fatalf("comment = %d %s", w.Code, w.Body.String())
	}
	// resolve is final
	if w := call(api, "itest-al1", "admin", "POST", "/v1/alerts/itest-al1-a/resolve", ""); w.Code != 200 {
		t.Fatalf("resolve = %d", w.Code)
	}
	if w := call(api, "itest-al1", "admin", "POST", "/v1/alerts/itest-al1-a/ack", ""); w.Code != 409 {
		t.Fatalf("ack after resolve = %d, want 409", w.Code)
	}
	if w := call(api, "itest-al1", "admin", "POST", "/v1/alerts/itest-al1-a/resolve", ""); w.Code != 409 {
		t.Fatalf("double resolve = %d, want 409", w.Code)
	}
	w := call(api, "itest-al1", "viewer", "GET", "/v1/alerts/itest-al1-a", "")
	b := w.Body.String()
	if w.Code != 200 || !strings.Contains(b, `"status":"resolved"`) || !strings.Contains(b, "checking the boiler") || !strings.Contains(b, `"acknowledged_by":"test-user"`) {
		t.Fatalf("detail wrong: %d %s", w.Code, b)
	}
	// status filter
	if w := call(api, "itest-al1", "viewer", "GET", "/v1/alerts?status=open", ""); strings.Contains(w.Body.String(), "itest-al1-a") {
		t.Fatalf("resolved alert listed as open: %s", w.Body.String())
	}
	if w := call(api, "itest-al1", "viewer", "GET", "/v1/alerts?status=bogus", ""); w.Code != 400 {
		t.Fatalf("bad status = %d, want 400", w.Code)
	}
}

func TestIntegrationRetentionPolicyAPI(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-ret")
	seed(t, s, "itest-ret2")
	s.st.Pool.Exec(t.Context(), `DELETE FROM tenant_retention WHERE tenant_id LIKE 'itest-ret%'`)
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/retention", s.getRetention)
	api.HandleFunc("PUT /v1/retention", s.putRetention)
	if w := call(api, "itest-ret", "viewer", "PUT", "/v1/retention", `{"raw_days":30}`); w.Code != 403 {
		t.Fatalf("viewer PUT = %d, want 403", w.Code)
	}
	for _, body := range []string{`{"raw_days":0}`, `{"hourly_days":10}`, `{"raw_days":90,"hourly_days":60}`} {
		if w := call(api, "itest-ret", "admin", "PUT", "/v1/retention", body); w.Code != 400 {
			t.Fatalf("%s = %d, want 400", body, w.Code)
		}
	}
	if w := call(api, "itest-ret", "admin", "PUT", "/v1/retention", `{"raw_days":30,"hourly_days":400}`); w.Code != 200 {
		t.Fatalf("valid PUT = %d %s", w.Code, w.Body.String())
	}
	w := call(api, "itest-ret", "viewer", "GET", "/v1/retention", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"raw_days":30`) {
		t.Fatalf("GET = %d %s", w.Code, w.Body.String())
	}
	if w := call(api, "itest-ret2", "viewer", "GET", "/v1/retention", ""); !strings.Contains(w.Body.String(), `"raw_days":null`) {
		t.Fatalf("other tenant sees policy: %s", w.Body.String())
	}
}

func TestIntegrationReportUsesDailyRollupAfterHourlyPurge(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-dly")
	ctx := t.Context()
	exec := func(q string, a ...any) {
		if _, err := s.st.Pool.Exec(ctx, q, a...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`DELETE FROM telemetry_rollup_hourly WHERE tenant_id='itest-dly'`)
	exec(`DELETE FROM telemetry_rollup_daily WHERE tenant_id='itest-dly'`)
	day := time.Now().UTC().Truncate(24 * time.Hour).Add(-30 * 24 * time.Hour)
	// 30 days ago only a daily row exists (hourly purged); yesterday has hourly rows.
	exec(`INSERT INTO telemetry_rollup_daily(tenant_id,device_id,point_id,bucket,n,sum,min,max) VALUES('itest-dly','d1','p1',$1,24,240,5,15)`, day)
	yday := time.Now().UTC().Truncate(24 * time.Hour).Add(-24 * time.Hour)
	exec(`INSERT INTO telemetry_rollup_hourly(tenant_id,device_id,point_id,bucket,n,sum,min,max) VALUES('itest-dly','d1','p1',$1,2,40,10,30)`, yday.Add(time.Hour))
	def := report.Definition{Metrics: []report.Metric{{DeviceID: "d1", PointID: "p1"}}, WindowHours: 24 * 45, GroupBy: "day"}
	series, total, err := s.buildSeries(ctx, "itest-dly", def)
	if err != nil {
		t.Fatal(err)
	}
	rows := series[def.Metrics[0]]
	if total != 2 || len(rows) != 2 {
		t.Fatalf("rows = %d %+v", len(rows), rows)
	}
	if rows[0].Count != 24 || rows[0].Sum != 240 || rows[0].Min != 5 || rows[0].Max != 15 {
		t.Fatalf("daily bucket wrong: %+v", rows[0])
	}
	if rows[1].Count != 2 || rows[1].Avg != 20 {
		t.Fatalf("hourly-derived bucket wrong: %+v", rows[1])
	}
	// hourly grouping must NOT pull daily rows
	def.GroupBy = "hour"
	series, _, _ = s.buildSeries(ctx, "itest-dly", def)
	if len(series[def.Metrics[0]]) != 1 {
		t.Fatalf("hour grouping used daily rows: %+v", series[def.Metrics[0]])
	}
}
