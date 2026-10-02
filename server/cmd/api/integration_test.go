package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

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
	mux.HandleFunc("POST /v1/reports/preview", s.previewReport)
	mux.HandleFunc("POST /v1/reports", s.createReport)
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
