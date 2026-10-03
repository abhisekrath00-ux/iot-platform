package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/rules"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/report"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
)

// Integration tests run against a real Postgres (CI service container, or
// TEST_DATABASE_URL locally) and skip otherwise. They cover the SQL paths the
// pure unit tests cannot: onboarding -> edge config, reports, exports, and
// tenant isolation.

func testServer(t testing.TB) (*server, http.Handler) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	t.Setenv("MIGRATIONS_DIR", "../../migrations")
	if err := migrate(ctx, st); err != nil {
		t.Fatal(err)
	}
	s := &server{st: st}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/profiles", s.createProfile)
	mux.HandleFunc("GET /v1/points", s.listPoints)
	mux.HandleFunc("GET /v1/gateways/{id}/edge-config", s.gatewayEdgeConfig)
	mux.HandleFunc("POST /v1/commissioning/sessions/{id}/profile", s.assignCommissionProfile)
	mux.HandleFunc("GET /v1/gateways/{id}/scans", s.listScans)
	mux.HandleFunc("GET /v1/telemetry/series", s.seriesTelemetry)
	mux.HandleFunc("GET /v1/devices/{id}/shadow", s.getShadow)
	mux.HandleFunc("PUT /v1/devices/{id}/attributes", s.setDeviceAttributes)
	mux.HandleFunc("GET /v1/alerts", s.listAlerts)
	mux.HandleFunc("GET /v1/alerts/{id}", s.getAlert)
	mux.HandleFunc("POST /v1/alerts/{id}/assign", s.assignAlert)
	mux.HandleFunc("GET /v1/assignees", s.listAssignees)
	mux.HandleFunc("GET /v1/escalation", s.getEscalation)
	mux.HandleFunc("PUT /v1/escalation", s.putEscalation)
	mux.HandleFunc("POST /v1/reports/preview", s.previewReport)
	mux.HandleFunc("POST /v1/reports", s.createReport)
	mux.HandleFunc("PUT /v1/reports/{id}", s.updateReport)
	mux.HandleFunc("GET /v1/reports/{id}/versions", s.listReportVersions)
	mux.HandleFunc("POST /v1/reports/{id}/versions/{v}/restore", s.restoreReportVersion)
	mux.HandleFunc("GET /v1/reports/{id}/download", s.downloadReport)
	mux.HandleFunc("GET /v1/export/telemetry.csv", s.exportTelemetryCSV)
	return s, mux
}

func call(h http.Handler, tenant, role, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	ctx := context.WithValue(r.Context(), auth.CtxTenant, tenant)
	ctx = context.WithValue(ctx, auth.CtxUser, "test-user")
	ctx = context.WithValue(ctx, auth.CtxRole, role)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r.WithContext(ctx))
	return w
}

func seed(t testing.TB, s *server, tenant string) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO tenants(id,name) VALUES('` + tenant + `','` + tenant + `') ON CONFLICT DO NOTHING`,
		// handlers record created_by = "test-user"; a fresh database has no such user (FK)
		`INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('test-user','` + tenant + `','test-user@example.invalid','test','admin') ON CONFLICT DO NOTHING`,
		`INSERT INTO sites(id,tenant_id,name) VALUES('` + tenant + `-site','` + tenant + `','S') ON CONFLICT DO NOTHING`,
		`INSERT INTO gateways(id,tenant_id,site_id,serial,status) VALUES('` + tenant + `-gw','` + tenant + `','` + tenant + `-site','SER-` + tenant + `','active') ON CONFLICT DO NOTHING`,
		`DELETE FROM telemetry WHERE tenant_id='` + tenant + `'`,
		`DELETE FROM telemetry_rollup_hourly WHERE tenant_id='` + tenant + `'`,
		`DELETE FROM points WHERE device_id IN (SELECT id FROM devices WHERE tenant_id='` + tenant + `')`,
		`DELETE FROM devices WHERE tenant_id='` + tenant + `'`,
		`INSERT INTO devices(id,tenant_id,gateway_id,profile,name,config) VALUES('` + tenant + `-dev','` + tenant + `','` + tenant + `-gw','modbus-tcp','Boiler PLC',
		   '{"connection":{"host":"10.0.0.7","address":1,"interval_seconds":5}}')`,
		`INSERT INTO points(id,device_id,unit,min_value,max_value) VALUES('temp','` + tenant + `-dev','C',-40,150)`,
		`INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,value,unit,schema_version)
		 SELECT '` + tenant + `-e'||g, '` + tenant + `','` + tenant + `-gw','` + tenant + `-dev','temp', now() - (g||' minutes')::interval, 20+g, 'C', 1 FROM generate_series(1,5) g`,
	} {
		if _, err := s.st.Pool.Exec(ctx, q); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
}

// Regression: the same point name on different devices (and tenants) must
// coexist; points.id alone used to be the primary key.
func TestIntegrationSamePointNameOnManyDevices(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-e")
	seed(t, s, "itest-f") // both seed a point called 'temp'
	var n int
	if err := s.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM points WHERE id='temp' AND device_id IN ('itest-e-dev','itest-f-dev')`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("points named temp = %d err=%v, want 2", n, err)
	}
}

func TestIntegrationPointsExportAndIsolation(t *testing.T) {
	s, h := testServer(t)
	seed(t, s, "itest-a")
	seed(t, s, "itest-b")

	w := call(h, "itest-a", "viewer", "GET", "/v1/points", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "itest-a-dev") || strings.Contains(w.Body.String(), "itest-b-dev") {
		t.Fatalf("points wrong or leaks tenants: %d %s", w.Code, w.Body.String())
	}
	w = call(h, "itest-a", "viewer", "GET", "/v1/export/telemetry.csv?device_id=itest-a-dev&hours=1", "")
	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	if w.Code != 200 || len(lines) != 6 || !strings.HasPrefix(lines[0], "observed_at,point_id") {
		t.Fatalf("csv export: %d %q", w.Code, w.Body.String())
	}
	// tenant B asking for tenant A's device gets header only
	w = call(h, "itest-b", "viewer", "GET", "/v1/export/telemetry.csv?device_id=itest-a-dev&hours=1", "")
	if n := len(strings.Split(strings.TrimSpace(w.Body.String()), "\n")); n != 1 {
		t.Fatalf("cross-tenant export leaked %d lines", n)
	}
	// and cannot fetch tenant A's gateway config
	if w = call(h, "itest-b", "admin", "GET", "/v1/gateways/itest-a-gw/edge-config", ""); w.Code != 404 {
		t.Fatalf("cross-tenant edge-config = %d, want 404", w.Code)
	}
}

func TestIntegrationProfileToEdgeConfig(t *testing.T) {
	s, h := testServer(t)
	seed(t, s, "itest-c")
	ctx := context.Background()
	s.st.Pool.Exec(ctx, `DELETE FROM commissioning_sessions WHERE tenant_id='itest-c'`)
	s.st.Pool.Exec(ctx, `DELETE FROM device_profiles WHERE tenant_id='itest-c'`)

	w := call(h, "itest-c", "admin", "POST", "/v1/profiles",
		`{"name":"Plant OPC","driver_profile":"opcua","points":[{"id":"boiler","node_id":"ns=2;s=Boiler.Temp","unit":"C","min":0,"max":400}]}`)
	if w.Code != 201 {
		t.Fatalf("create profile %d %s", w.Code, w.Body.String())
	}
	var created struct{ ID string }
	json.Unmarshal(w.Body.Bytes(), &created)
	// a bad node id is refused at the API, not on the gateway
	if w = call(h, "itest-c", "admin", "POST", "/v1/profiles",
		`{"name":"Bad","driver_profile":"opcua","points":[{"id":"x","node_id":"junk","min":0,"max":1}]}`); w.Code != 400 {
		t.Fatalf("bad node id = %d", w.Code)
	}
	if w = call(h, "itest-c", "viewer", "POST", "/v1/profiles", `{}`); w.Code != 403 {
		t.Fatalf("viewer create profile = %d, want 403", w.Code)
	}

	if _, err := s.st.Pool.Exec(ctx,
		`INSERT INTO commissioning_sessions(id,tenant_id,site_id,gateway_id,serial,created_by) VALUES('itest-c-sess','itest-c','itest-c-site','itest-c-gw','SER-itest-c','test-user')`); err != nil {
		t.Fatal(err)
	}
	w = call(h, "itest-c", "admin", "POST", "/v1/commissioning/sessions/itest-c-sess/profile",
		`{"profile_id":"`+created.ID+`","device_name":"Boiler OPC","connection":{"endpoint":"opc.tcp://10.0.0.9:4840","interval_seconds":10}}`)
	if w.Code != 201 {
		t.Fatalf("assign %d %s", w.Code, w.Body.String())
	}
	if w = call(h, "itest-c", "admin", "GET", "/v1/gateways/itest-c-gw/edge-config", ""); w.Code != 200 ||
		!strings.Contains(w.Body.String(), "opc.tcp://10.0.0.9:4840") || !strings.Contains(w.Body.String(), "ns=2;s=Boiler.Temp") {
		t.Fatalf("edge config missing device: %d\n%s", w.Code, w.Body.String())
	}
}

func TestIntegrationReportPreviewAndDownload(t *testing.T) {
	s, h := testServer(t)
	seed(t, s, "itest-d")
	def := `{"metrics":[{"device_id":"itest-d-dev","point_id":"temp"}],"window_hours":2,"group_by":"hour"}`
	w := call(h, "itest-d", "viewer", "POST", "/v1/reports/preview", `{"name":"P","definition":`+def+`}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"rows":`) || strings.Contains(w.Body.String(), `"rows":0`) {
		t.Fatalf("preview %d %s", w.Code, w.Body.String())
	}
	w = call(h, "itest-d", "admin", "POST", "/v1/reports", `{"name":"R","definition":`+def+`}`)
	if w.Code != 201 && w.Code != 200 {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var rep struct {
		ID string `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &rep)
	w = call(h, "itest-d", "viewer", "GET", "/v1/reports/"+rep.ID+"/download?format=csv", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "itest-d-dev,temp,") {
		t.Fatalf("download %d %s", w.Code, w.Body.String())
	}
	w = call(h, "itest-d", "viewer", "GET", "/v1/reports/"+rep.ID+"/download?format=csv&layout=matrix&agg=max&group_by=day", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"itest-d-dev / temp"`) {
		t.Fatalf("matrix csv %d %s", w.Code, w.Body.String())
	}
	if w = call(h, "itest-d", "viewer", "GET", "/v1/reports/"+rep.ID+"/download?format=csv&device=itest-d-dev", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "itest-d-dev,temp,") {
		t.Fatalf("device param (same device) %d %s", w.Code, w.Body.String())
	}
	if w = call(h, "itest-d", "viewer", "GET", "/v1/reports/"+rep.ID+"/download?format=csv&device=other-dev", ""); w.Code != 200 || strings.Contains(w.Body.String(), "itest-d-dev,temp,") {
		t.Fatalf("device param must swap the device: %d %s", w.Code, w.Body.String())
	}
	if w = call(h, "itest-d", "viewer", "GET", "/v1/reports/"+rep.ID+"/download?format=csv&device=Bad%20Id", ""); w.Code != 400 {
		t.Fatalf("bad device = %d, want 400", w.Code)
	}
	if w = call(h, "itest-d", "viewer", "GET", "/v1/reports/"+rep.ID+"/download?format=csv&group_by=year", ""); w.Code != 400 {
		t.Fatalf("bad parameter = %d, want 400", w.Code)
	}
	w = call(h, "itest-d", "viewer", "GET", "/v1/reports/"+rep.ID+"/download?format=pdf", "")
	if w.Code != 200 || !strings.HasPrefix(w.Body.String(), "%PDF-1.4") || w.Header().Get("Content-Type") != "application/pdf" {
		t.Fatalf("pdf download %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	w = call(h, "itest-d", "viewer", "GET", "/v1/reports/"+rep.ID+"/download?format=xlsx", "")
	if w.Code != 200 || !strings.HasPrefix(w.Body.String(), "PK") {
		t.Fatalf("xlsx download %d", w.Code)
	}
	for _, f := range []string{"csv", "pdf", "xlsx"} {
		if w = call(h, "itest-other", "viewer", "GET", "/v1/reports/"+rep.ID+"/download?format="+f, ""); w.Code != 404 {
			t.Fatalf("cross-tenant %s download = %d", f, w.Code)
		}
	}
}

// Every allowed bucket size must execute against real Postgres and account
// for all samples (catches SQL-expression mistakes in BucketExpr).
func TestIntegrationReportBucketSizes(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-g")
	for _, g := range []string{"15min", "hour", "day", "week"} {
		def := report.Definition{Metrics: []report.Metric{{DeviceID: "itest-g-dev", PointID: "temp"}}, WindowHours: 24, GroupBy: g}
		series, total, err := s.buildSeries(context.Background(), "itest-g", def)
		if err != nil || total == 0 {
			t.Fatalf("%s: total=%d err=%v", g, total, err)
		}
		_, _, _, sum, n := report.Summary(series[def.Metrics[0]])
		if n != 5 || sum != 21+22+23+24+25 {
			t.Fatalf("%s: samples=%d sum=%v, want 5 and 115", g, n, sum)
		}
	}
}

// Rollup by asset and by site: names come from the database, scoped to the
// caller's tenant, and unassigned devices are grouped separately.
func TestIntegrationReportRollup(t *testing.T) {
	s, h := testServer(t)
	seed(t, s, "itest-rg")
	seed(t, s, "itest-rg2")
	ctx := context.Background()
	for _, q := range []string{
		`DELETE FROM assets WHERE tenant_id='itest-rg'`,
		`INSERT INTO assets(id,tenant_id,name) VALUES('itest-rg-asset','itest-rg','Line A')`,
		`UPDATE devices SET asset_id='itest-rg-asset' WHERE id='itest-rg-dev'`,
	} {
		if _, err := s.st.Pool.Exec(ctx, q); err != nil {
			t.Fatalf("%q: %v", q, err)
		}
	}
	def := func(rollup string, dev string) string {
		return `{"metrics":[{"device_id":"` + dev + `","point_id":"temp"}],"window_hours":2,"group_by":"hour","rollup":"` + rollup + `"}`
	}
	w := call(h, "itest-rg", "viewer", "POST", "/v1/reports/preview", `{"name":"P","definition":`+def("asset", "itest-rg-dev")+`}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Summary by asset") || !strings.Contains(w.Body.String(), "Line A") || !strings.Contains(w.Body.String(), "Total (all groups)") {
		t.Fatalf("asset rollup: %d %s", w.Code, w.Body.String())
	}
	w = call(h, "itest-rg", "viewer", "POST", "/v1/reports/preview", `{"name":"P","definition":`+def("site", "itest-rg-dev")+`}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Summary by site") || !strings.Contains(w.Body.String(), `\u003ctd\u003eS\u003c/td\u003e`) {
		t.Fatalf("site rollup: %d %s", w.Code, w.Body.String())
	}
	// a device with no asset lands in (unassigned)
	w = call(h, "itest-rg2", "viewer", "POST", "/v1/reports/preview", `{"name":"P","definition":`+def("asset", "itest-rg2-dev")+`}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "(unassigned)") || strings.Contains(w.Body.String(), "Line A") {
		t.Fatalf("unassigned: %d %s", w.Code, w.Body.String())
	}
	// another tenant naming this tenant's device sees neither data nor the asset name
	w = call(h, "itest-rg2", "viewer", "POST", "/v1/reports/preview", `{"name":"P","definition":`+def("asset", "itest-rg-dev")+`}`)
	if w.Code != 200 || strings.Contains(w.Body.String(), "Line A") {
		t.Fatalf("cross-tenant leak: %d %s", w.Code, w.Body.String())
	}
	if w = call(h, "itest-rg", "viewer", "POST", "/v1/reports/preview", `{"name":"P","definition":`+def("floor", "itest-rg-dev")+`}`); w.Code != 400 {
		t.Fatalf("bad rollup = %d, want 400", w.Code)
	}
	// stored reports keep the rollup and downloads include it
	w = call(h, "itest-rg", "admin", "POST", "/v1/reports", `{"name":"R","definition":`+def("asset", "itest-rg-dev")+`}`)
	var rep struct {
		ID string `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &rep)
	w = call(h, "itest-rg", "viewer", "GET", "/v1/reports/"+rep.ID+"/download?format=csv", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"Line A","temp","1","5"`) {
		t.Fatalf("csv rollup %d %s", w.Code, w.Body.String())
	}
}

func TestIntegrationAutoScanFromEdge(t *testing.T) {
	s, h := testServer(t)
	seed(t, s, "itest-as")
	seed(t, s, "itest-as2")
	ctx := context.Background()
	if _, err := s.st.Pool.Exec(ctx, `DELETE FROM gateway_scans WHERE tenant_id IN ('itest-as','itest-as2')`); err != nil {
		t.Fatal(err)
	}
	pay := func(id, kind string) []byte {
		return []byte(`{"scan_id":"` + id + `","kind":"` + kind + `","ok":true,"auto":true,"params":{"port":"/dev/ttyUSB0","baud":9600,"parity":"none"},"slaves":[{"address":3,"matches":[]}],"finished_at":"2026-10-02T00:00:00Z"}`)
	}
	if err := s.st.StoreAutoScan(ctx, "itest-as", "itest-as-gw", pay("auto-abc-1", "modbus-rtu")); err != nil {
		t.Fatal(err)
	}
	// refused: wrong tenant for that gateway, bad prefix, unknown kind, duplicate id
	for name, err := range map[string]error{
		"cross tenant": s.st.StoreAutoScan(ctx, "itest-as2", "itest-as-gw", pay("auto-abc-2", "modbus-rtu")),
		"not auto":     s.st.StoreAutoScan(ctx, "itest-as", "itest-as-gw", pay("scan-abc-3", "modbus-rtu")),
		"bad kind":     s.st.StoreAutoScan(ctx, "itest-as", "itest-as-gw", pay("auto-abc-4", "control")),
		"duplicate":    s.st.StoreAutoScan(ctx, "itest-as", "itest-as-gw", pay("auto-abc-1", "modbus-rtu")),
	} {
		if err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	w := call(h, "itest-as", "viewer", "GET", "/v1/gateways/itest-as-gw/scans", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"auto":true`) || !strings.Contains(w.Body.String(), `"status":"done"`) || strings.Contains(w.Body.String(), "auto-abc-2") {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	// volume cap: the 30th in an hour passes, the 31st does not
	for i := 0; i < 40; i++ {
		_ = s.st.StoreAutoScan(ctx, "itest-as", "itest-as-gw", pay(fmt.Sprintf("auto-cap-%d", i), "lan"))
	}
	var n int
	s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM gateway_scans WHERE tenant_id='itest-as'`).Scan(&n)
	if n > 20 {
		t.Fatalf("kept %d auto rows, want at most 20", n)
	}
}

func TestIntegrationSeriesRanges(t *testing.T) {
	s, h := testServer(t)
	seed(t, s, "itest-sr")
	ctx := context.Background()
	for _, q := range []string{
		`DELETE FROM telemetry_rollup_daily WHERE tenant_id='itest-sr'`,
		`DELETE FROM telemetry_rollup_hourly WHERE tenant_id='itest-sr'`,
		`INSERT INTO telemetry_rollup_daily(tenant_id,device_id,point_id,bucket,n,sum,min,max) VALUES('itest-sr','itest-sr-dev','temp',date_trunc('day', now()) - interval '60 days',10,200,15,25)`,
	} {
		if _, err := s.st.Pool.Exec(ctx, q); err != nil {
			t.Fatalf("%q: %v", q, err)
		}
	}
	get := func(qs string) *httptest.ResponseRecorder {
		return call(h, "itest-sr", "viewer", "GET", "/v1/telemetry/series?device_id=itest-sr-dev&point_id=temp"+qs, "")
	}
	if w := get(""); w.Code != 200 || strings.Contains(w.Body.String(), "aggregated") {
		t.Fatalf("default stays raw: %d %s", w.Code, w.Body.String())
	}
	w := get("&hours=2160")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"aggregated":true`) || !strings.Contains(w.Body.String(), `"v":20`) || !strings.Contains(w.Body.String(), `"min":15`) {
		t.Fatalf("90 day range should read the daily rollup: %d %s", w.Code, w.Body.String())
	}
	for _, bad := range []string{"&hours=0", "&hours=9000", "&hours=abc"} {
		if w := get(bad); w.Code != 400 {
			t.Errorf("%s: %d", bad, w.Code)
		}
	}
	// another tenant sees nothing
	w = call(h, "itest-sr2", "viewer", "GET", "/v1/telemetry/series?device_id=itest-sr-dev&point_id=temp&hours=2160", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), `"v":20`) {
		t.Fatalf("tenant isolation: %d %s", w.Code, w.Body.String())
	}
}

type fakeNotifier struct{ emails []string }

// Email records only messages to this test's own recipient: the sweeper is
// global, so other tenants' policies in a shared database must not leak in.
func (f *fakeNotifier) Email(_ context.Context, to []string, subject, body string) error {
	if to[0] == "boss@example.com" {
		f.emails = append(f.emails, to[0]+"|"+body)
	}
	return nil
}
func (f *fakeNotifier) Slack(context.Context, string, string) error { return nil }

func TestIntegrationEscalation(t *testing.T) {
	s, h := testServer(t)
	seed(t, s, "itest-es")
	seed(t, s, "itest-es2")
	ctx := context.Background()
	for _, q := range []string{
		`DELETE FROM escalation_steps WHERE tenant_id IN ('itest-es','itest-es2')`,
		`DELETE FROM alerts WHERE tenant_id='itest-es'`,
		`DELETE FROM notification_channels WHERE tenant_id IN ('itest-es','itest-es2')`,
		`INSERT INTO notification_channels(id,tenant_id,type,target) VALUES('itest-es-ch','itest-es','email','boss@example.com')`,
		`INSERT INTO notification_channels(id,tenant_id,type,target) VALUES('itest-es2-ch','itest-es2','email','other@example.com')`,
		`INSERT INTO alerts(id,tenant_id,severity,message,created_at) VALUES('itest-es-a1','itest-es','warning','Boiler hot', now() - interval '20 minutes')`,
		`INSERT INTO alerts(id,tenant_id,severity,message,created_at) VALUES('itest-es-a2','itest-es','warning','Fan stuck', now() - interval '20 minutes')`,
	} {
		if _, err := s.st.Pool.Exec(ctx, q); err != nil {
			t.Fatalf("%q: %v", q, err)
		}
	}
	policy := `{"steps":[{"severity":"","step":1,"after_minutes":10,"channel_id":"itest-es-ch"},{"severity":"","step":2,"after_minutes":60,"channel_id":"itest-es-ch"}]}`
	if w := call(h, "itest-es", "viewer", "PUT", "/v1/escalation", policy); w.Code != 403 {
		t.Fatalf("viewer may not change the policy: %d", w.Code)
	}
	if w := call(h, "itest-es", "admin", "PUT", "/v1/escalation", `{"steps":[{"step":1,"after_minutes":10,"channel_id":"itest-es2-ch"}]}`); w.Code != 400 {
		t.Fatalf("another tenant's channel must be refused: %d %s", w.Code, w.Body.String())
	}
	if w := call(h, "itest-es", "admin", "PUT", "/v1/escalation", policy); w.Code != 200 {
		t.Fatalf("put: %d %s", w.Code, w.Body.String())
	}
	if w := call(h, "itest-es", "viewer", "GET", "/v1/escalation", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"after_minutes":60`) {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
	// acknowledge a2: it must never escalate
	if _, err := s.st.Pool.Exec(ctx, `UPDATE alerts SET status='acknowledged' WHERE id='itest-es-a2'`); err != nil {
		t.Fatal(err)
	}
	fn := &fakeNotifier{}
	rules.EvaluateEscalations(ctx, s.st.Pool, fn)
	if len(fn.emails) != 1 || !strings.Contains(fn.emails[0], "boss@example.com") || !strings.Contains(fn.emails[0], "ESCALATION step 1") || !strings.Contains(fn.emails[0], "Boiler hot") {
		t.Fatalf("emails = %v", fn.emails)
	}
	rules.EvaluateEscalations(ctx, s.st.Pool, fn)
	if len(fn.emails) != 1 {
		t.Fatalf("step 1 repeated or step 2 sent early: %v", fn.emails)
	}
	// later: step 2 is due
	if _, err := s.st.Pool.Exec(ctx, `UPDATE alerts SET created_at = now() - interval '90 minutes' WHERE id='itest-es-a1'`); err != nil {
		t.Fatal(err)
	}
	rules.EvaluateEscalations(ctx, s.st.Pool, fn)
	if len(fn.emails) != 2 || !strings.Contains(fn.emails[1], "ESCALATION step 2") {
		t.Fatalf("emails = %v", fn.emails)
	}
	var lvl int
	s.st.Pool.QueryRow(ctx, `SELECT escalation_level FROM alerts WHERE id='itest-es-a1'`).Scan(&lvl)
	if lvl != 2 {
		t.Fatalf("level = %d", lvl)
	}
	rules.EvaluateEscalations(ctx, s.st.Pool, fn)
	if len(fn.emails) != 2 {
		t.Fatal("no steps left, nothing more to send")
	}
}

func TestIntegrationEscalationRepeat(t *testing.T) {
	s, h := testServer(t)
	seed(t, s, "itest-er")
	ctx := context.Background()
	for _, q := range []string{
		`DELETE FROM escalation_steps WHERE tenant_id='itest-er'`,
		`DELETE FROM alerts WHERE tenant_id='itest-er'`,
		`DELETE FROM notification_channels WHERE tenant_id='itest-er'`,
		`INSERT INTO notification_channels(id,tenant_id,type,target) VALUES('itest-er-ch','itest-er','email','boss@example.com')`,
		`INSERT INTO alerts(id,tenant_id,severity,message,created_at) VALUES('itest-er-a1','itest-er','warning','Pump down', now() - interval '60 minutes')`,
	} {
		if _, err := s.st.Pool.Exec(ctx, q); err != nil {
			t.Fatalf("%q: %v", q, err)
		}
	}
	step := `{"steps":[{"step":1,"after_minutes":10,"channel_id":"itest-er-ch"}]`
	if w := call(h, "itest-er", "admin", "PUT", "/v1/escalation", step+`,"repeat":{"every_minutes":2,"max":1}}`); w.Code != 400 {
		t.Fatalf("bad repeat accepted: %d", w.Code)
	}
	if w := call(h, "itest-er", "viewer", "PUT", "/v1/escalation", step+`,"repeat":{"every_minutes":15,"max":2}}`); w.Code != 403 {
		t.Fatalf("viewer: %d", w.Code)
	}
	if w := call(h, "itest-er", "admin", "PUT", "/v1/escalation", step+`,"repeat":{"every_minutes":15,"max":2}}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"every_minutes":15`) {
		t.Fatalf("put: %d %s", w.Code, w.Body.String())
	}
	// a client that omits "repeat" keeps the setting
	if w := call(h, "itest-er", "admin", "PUT", "/v1/escalation", step+`}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"max":2`) {
		t.Fatalf("omitted repeat must keep the setting: %d %s", w.Code, w.Body.String())
	}
	fn := &fakeNotifier{}
	rules.EvaluateEscalations(ctx, s.st.Pool, fn) // step 1
	rules.EvaluateEscalations(ctx, s.st.Pool, fn) // too soon for a reminder
	if len(fn.emails) != 1 {
		t.Fatalf("emails = %v", fn.emails)
	}
	for i := 1; i <= 3; i++ {
		s.st.Pool.Exec(ctx, `UPDATE alerts SET escalated_at = now() - interval '16 minutes' WHERE id='itest-er-a1'`)
		rules.EvaluateEscalations(ctx, s.st.Pool, fn)
	}
	// max 2 reminders, then it stops
	if len(fn.emails) != 3 || !strings.Contains(fn.emails[1], "REMINDER 1 of 2") || !strings.Contains(fn.emails[2], "REMINDER 2 of 2") {
		t.Fatalf("emails = %v", fn.emails)
	}
	// acknowledging stops it even before max
	s.st.Pool.Exec(ctx, `UPDATE alerts SET status='acknowledged', escalation_repeats=0 WHERE id='itest-er-a1'`)
	s.st.Pool.Exec(ctx, `UPDATE alerts SET escalated_at = now() - interval '99 minutes' WHERE id='itest-er-a1'`)
	rules.EvaluateEscalations(ctx, s.st.Pool, fn)
	if len(fn.emails) != 3 {
		t.Fatalf("acknowledged alert still reminded: %v", fn.emails)
	}
}

func TestIntegrationShadow(t *testing.T) {
	s, h := testServer(t)
	seed(t, s, "itest-sh")
	seed(t, s, "itest-sh2")
	// seed gives 'temp' readings 1..5 minutes old; the newest (1 minute, value 21) must win, and with a
	// 5 s polling interval (30 s floor) a 1-minute-old reading is stale
	w := call(h, "itest-sh", "viewer", "GET", "/v1/devices/itest-sh-dev/shadow", "")
	var out struct {
		Reported map[string]struct {
			Value float64
			Stale bool
		}
		Attributes map[string]any
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Reported["temp"].Value != 21 || !out.Reported["temp"].Stale {
		t.Fatalf("shadow: %d %s", w.Code, w.Body.String())
	}
	if w := call(h, "itest-sh", "viewer", "PUT", "/v1/devices/itest-sh-dev/attributes", `{"attributes":{"floor":2}}`); w.Code != 403 {
		t.Fatalf("viewer set attributes: %d", w.Code)
	}
	if w := call(h, "itest-sh", "operator", "PUT", "/v1/devices/itest-sh-dev/attributes", `{"attributes":{"nested":{"a":1}}}`); w.Code != 400 {
		t.Fatalf("nested attribute: %d", w.Code)
	}
	if w := call(h, "itest-sh", "operator", "PUT", "/v1/devices/itest-sh-dev/attributes", `{"attributes":{"floor":2,"owner":"plant team"}}`); w.Code != 200 {
		t.Fatalf("set attributes: %d %s", w.Code, w.Body.String())
	}
	w = call(h, "itest-sh", "viewer", "GET", "/v1/devices/itest-sh-dev/shadow", "")
	if !strings.Contains(w.Body.String(), `"owner":"plant team"`) {
		t.Fatalf("attributes missing: %s", w.Body.String())
	}
	// another tenant sees nothing and cannot write
	if w := call(h, "itest-sh2", "viewer", "GET", "/v1/devices/itest-sh-dev/shadow", ""); w.Code != 404 {
		t.Fatalf("cross-tenant shadow: %d", w.Code)
	}
	if w := call(h, "itest-sh2", "admin", "PUT", "/v1/devices/itest-sh-dev/attributes", `{"attributes":{"x":1}}`); w.Code != 404 {
		t.Fatalf("cross-tenant attributes: %d", w.Code)
	}
}

func TestIntegrationEscalationQuietHours(t *testing.T) {
	s, h := testServer(t)
	seed(t, s, "itest-eq")
	ctx := context.Background()
	for _, q := range []string{
		`DELETE FROM escalation_steps WHERE tenant_id='itest-eq'`,
		`DELETE FROM alerts WHERE tenant_id='itest-eq'`,
		`DELETE FROM notification_channels WHERE tenant_id='itest-eq'`,
		`INSERT INTO notification_channels(id,tenant_id,type,target) VALUES('itest-eq-ch','itest-eq','email','boss@example.com')`,
		// both alerts already had their only step sent and are due a reminder
		`INSERT INTO alerts(id,tenant_id,severity,message,created_at,escalation_level,escalated_at) VALUES('itest-eq-w','itest-eq','warning','Warn pump',now()-interval '2 hours',1,now()-interval '1 hour')`,
		`INSERT INTO alerts(id,tenant_id,severity,message,created_at,escalation_level,escalated_at) VALUES('itest-eq-c','itest-eq','critical','Crit pump',now()-interval '2 hours',1,now()-interval '1 hour')`,
	} {
		if _, err := s.st.Pool.Exec(ctx, q); err != nil {
			t.Fatalf("%q: %v", q, err)
		}
	}
	now := time.Now().UTC()
	win := fmt.Sprintf(`"start":"%s","end":"%s","timezone":"UTC"`, now.Add(-time.Hour).Format("15:04"), now.Add(time.Hour).Format("15:04"))
	body := func(quiet string) string {
		return `{"steps":[{"step":1,"after_minutes":10,"channel_id":"itest-eq-ch"}],"repeat":{"every_minutes":15,"max":3}` + quiet + `}`
	}
	if w := call(h, "itest-eq", "admin", "PUT", "/v1/escalation", body(`,"quiet":{"start":"22:00","end":"06:00","timezone":"Nowhere/Land"}`)); w.Code != 400 {
		t.Fatalf("bad zone accepted: %d", w.Code)
	}
	if w := call(h, "itest-eq", "viewer", "PUT", "/v1/escalation", body(`,"quiet":{`+win+`}`)); w.Code != 403 {
		t.Fatalf("viewer: %d", w.Code)
	}
	if w := call(h, "itest-eq", "admin", "PUT", "/v1/escalation", body(`,"quiet":{`+win+`}`)); w.Code != 200 || !strings.Contains(w.Body.String(), `"timezone":"UTC"`) {
		t.Fatalf("put: %d %s", w.Code, w.Body.String())
	}
	fn := &fakeNotifier{}
	rules.EvaluateEscalations(ctx, s.st.Pool, fn)
	// inside quiet hours: the warning reminder is held back, the critical one is not
	if len(fn.emails) != 1 || !strings.Contains(fn.emails[0], "Crit pump") {
		t.Fatalf("emails = %v", fn.emails)
	}
	// a client that omits "quiet" keeps it; clearing it releases the held reminder
	if w := call(h, "itest-eq", "admin", "PUT", "/v1/escalation", body(``)); w.Code != 200 || !strings.Contains(w.Body.String(), `"timezone":"UTC"`) {
		t.Fatalf("omitted quiet must keep it: %d %s", w.Code, w.Body.String())
	}
	if w := call(h, "itest-eq", "admin", "PUT", "/v1/escalation", body(`,"quiet":{"start":"","end":"","timezone":""}`)); w.Code != 200 {
		t.Fatalf("clear: %d %s", w.Code, w.Body.String())
	}
	rules.EvaluateEscalations(ctx, s.st.Pool, fn)
	if len(fn.emails) != 2 || !strings.Contains(fn.emails[1], "Warn pump") || !strings.Contains(fn.emails[1], "REMINDER") {
		t.Fatalf("held reminder not sent after quiet hours: %v", fn.emails)
	}
}

func TestIntegrationReportVersions(t *testing.T) {
	s, h := testServer(t)
	seed(t, s, "itest-rv")
	seed(t, s, "itest-rv2")
	def := func(hours int) string {
		return fmt.Sprintf(`{"metrics":[{"device_id":"itest-rv-dev","point_id":"temp"}],"window_hours":%d,"group_by":"hour"}`, hours)
	}
	w := call(h, "itest-rv", "admin", "POST", "/v1/reports", `{"name":"Daily","definition":`+def(24)+`}`)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var rep struct{ ID string }
	json.Unmarshal(w.Body.Bytes(), &rep)
	put := func(role, tenant, body string) *httptest.ResponseRecorder {
		return call(h, tenant, role, "PUT", "/v1/reports/"+rep.ID, body)
	}
	if w := put("viewer", "itest-rv", `{"name":"X","definition":`+def(48)+`}`); w.Code != 403 {
		t.Fatalf("viewer edit: %d", w.Code)
	}
	if w := put("admin", "itest-rv", `{"name":"X","definition":{"metrics":[]}}`); w.Code != 400 {
		t.Fatalf("invalid definition accepted: %d", w.Code)
	}
	if w := put("admin", "itest-rv2", `{"name":"X","definition":`+def(48)+`}`); w.Code != 404 {
		t.Fatalf("another tenant edited the report: %d", w.Code)
	}
	if w := put("operator", "itest-rv", `{"name":"Daily v2","definition":`+def(48)+`}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"version":2`) {
		t.Fatalf("edit 1: %d %s", w.Code, w.Body.String())
	}
	if w := put("admin", "itest-rv", `{"name":"Daily v3","definition":`+def(72)+`}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"version":3`) {
		t.Fatalf("edit 2: %d %s", w.Code, w.Body.String())
	}
	w = call(h, "itest-rv", "viewer", "GET", "/v1/reports/"+rep.ID+"/versions", "")
	var vs struct {
		Current  int
		Versions []struct {
			Version    int
			Name       string
			Definition struct{ Window_hours int }
		}
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &vs) != nil || vs.Current != 3 || len(vs.Versions) != 2 ||
		vs.Versions[0].Version != 2 || vs.Versions[0].Name != "Daily v2" || vs.Versions[0].Definition.Window_hours != 48 ||
		vs.Versions[1].Version != 1 || vs.Versions[1].Name != "Daily" {
		t.Fatalf("versions: %d %s", w.Code, w.Body.String())
	}
	if w := call(h, "itest-rv2", "viewer", "GET", "/v1/reports/"+rep.ID+"/versions", ""); w.Code != 404 {
		t.Fatalf("cross-tenant versions: %d", w.Code)
	}
	// restore version 1 as a new version 4; history keeps v3 as well
	if w := call(h, "itest-rv2", "admin", "POST", "/v1/reports/"+rep.ID+"/versions/1/restore", ""); w.Code != 404 {
		t.Fatalf("cross-tenant restore: %d", w.Code)
	}
	if w := call(h, "itest-rv", "viewer", "POST", "/v1/reports/"+rep.ID+"/versions/1/restore", ""); w.Code != 403 {
		t.Fatalf("viewer restore: %d", w.Code)
	}
	if w := call(h, "itest-rv", "admin", "POST", "/v1/reports/"+rep.ID+"/versions/9/restore", ""); w.Code != 404 {
		t.Fatalf("missing version: %d", w.Code)
	}
	if w := call(h, "itest-rv", "admin", "POST", "/v1/reports/"+rep.ID+"/versions/1/restore", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"version":4`) {
		t.Fatalf("restore: %d %s", w.Code, w.Body.String())
	}
	var name string
	var hours int
	s.st.Pool.QueryRow(context.Background(), `SELECT name, (definition->>'window_hours')::int FROM reports WHERE id=$1`, rep.ID).Scan(&name, &hours)
	var n int
	s.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM report_versions WHERE report_id=$1`, rep.ID).Scan(&n)
	if name != "Daily" || hours != 24 || n != 3 {
		t.Fatalf("after restore name=%q hours=%d saved=%d", name, hours, n)
	}
}

func TestIntegrationAlertAssignment(t *testing.T) {
	s, h := testServer(t)
	seed(t, s, "itest-as")
	seed(t, s, "itest-as2")
	ctx := context.Background()
	for _, q := range []string{
		`DELETE FROM alerts WHERE tenant_id IN ('itest-as','itest-as2')`,
		`INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('itest-as-op','itest-as','itest-as-op@example.invalid','Ola Operator','operator') ON CONFLICT DO NOTHING`,
		`INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('itest-as-vw','itest-as','itest-as-vw@example.invalid','Vik Viewer','viewer') ON CONFLICT DO NOTHING`,
		`INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('itest-as2-op','itest-as2','itest-as2-op@example.invalid','Other Tenant','operator') ON CONFLICT DO NOTHING`,
		`INSERT INTO alerts(id,tenant_id,severity,message) VALUES('itest-as-a1','itest-as','warning','Pump noisy')`,
		`INSERT INTO alerts(id,tenant_id,severity,message) VALUES('itest-as-a2','itest-as','info','Door open')`,
		`INSERT INTO alerts(id,tenant_id,severity,message,status) VALUES('itest-as-a3','itest-as','info','Old','resolved')`,
	} {
		if _, err := s.st.Pool.Exec(ctx, q); err != nil {
			t.Fatalf("%q: %v", q, err)
		}
	}
	assign := func(role, tenant, id, user string) *httptest.ResponseRecorder {
		return call(h, tenant, role, "POST", "/v1/alerts/"+id+"/assign", `{"user_id":"`+user+`"}`)
	}
	if w := assign("viewer", "itest-as", "itest-as-a1", "itest-as-op"); w.Code != 403 {
		t.Fatalf("viewer assign: %d", w.Code)
	}
	for name, user := range map[string]string{"viewer assignee": "itest-as-vw", "other tenant's user": "itest-as2-op", "unknown": "nobody"} {
		if w := assign("operator", "itest-as", "itest-as-a1", user); w.Code != 400 {
			t.Fatalf("%s accepted: %d", name, w.Code)
		}
	}
	if w := assign("operator", "itest-as2", "itest-as-a1", "itest-as2-op"); w.Code != 409 {
		t.Fatalf("another tenant's alert: %d", w.Code)
	}
	if w := assign("operator", "itest-as", "itest-as-a3", "itest-as-op"); w.Code != 409 {
		t.Fatalf("resolved alert assigned: %d", w.Code)
	}
	if w := assign("operator", "itest-as", "itest-as-a1", "itest-as-op"); w.Code != 200 {
		t.Fatalf("assign: %d %s", w.Code, w.Body.String())
	}
	w := call(h, "itest-as", "viewer", "GET", "/v1/alerts?status=open&assigned=itest-as-op", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "itest-as-a1") || strings.Contains(w.Body.String(), "itest-as-a2") {
		t.Fatalf("assigned filter: %d %s", w.Code, w.Body.String())
	}
	w = call(h, "itest-as", "viewer", "GET", "/v1/alerts?status=open&assigned=none", "")
	if !strings.Contains(w.Body.String(), "itest-as-a2") || strings.Contains(w.Body.String(), "itest-as-a1") {
		t.Fatalf("unassigned filter: %s", w.Body.String())
	}
	w = call(h, "itest-as", "viewer", "GET", "/v1/alerts/itest-as-a1", "")
	if !strings.Contains(w.Body.String(), `"assigned_to":"itest-as-op"`) || !strings.Contains(w.Body.String(), "Assigned to Ola Operator") {
		t.Fatalf("detail: %s", w.Body.String())
	}
	if w := call(h, "itest-as", "viewer", "GET", "/v1/assignees", ""); w.Code != 403 {
		t.Fatalf("viewer list assignees: %d", w.Code)
	}
	w = call(h, "itest-as", "operator", "GET", "/v1/assignees", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Ola Operator") || strings.Contains(w.Body.String(), "Vik Viewer") || strings.Contains(w.Body.String(), "Other Tenant") || strings.Contains(w.Body.String(), "example.invalid") {
		t.Fatalf("assignees: %d %s", w.Code, w.Body.String())
	}
	if w := assign("operator", "itest-as", "itest-as-a1", ""); w.Code != 200 {
		t.Fatalf("unassign: %d", w.Code)
	}
	w = call(h, "itest-as", "viewer", "GET", "/v1/alerts?status=open&assigned=itest-as-op", "")
	if strings.Contains(w.Body.String(), "itest-as-a1") {
		t.Fatalf("still assigned: %s", w.Body.String())
	}
}
