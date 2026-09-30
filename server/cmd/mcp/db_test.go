package main

// DB-backed checks of the tool SQL: tenant isolation, bounds, aggregation.
// Skipped without TEST_DATABASE_URL (CI provides it).

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
)

func dbServer(t *testing.T) *server {
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
	// Same advisory lock as cmd/api migrate, so parallel test packages serialize.
	conn, err := st.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", int64(0x4845584D4F4E)); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", int64(0x4845584D4F4E)) //nolint:errcheck
	entries, _ := os.ReadDir("../../migrations")
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		b, _ := os.ReadFile("../../migrations/" + e.Name())
		if _, err := conn.Exec(ctx, string(b)); err != nil {
			t.Fatalf("migration %s: %v", e.Name(), err)
		}
	}
	return &server{st: st}
}

func seedMCP(t *testing.T, s *server, tenant string) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO tenants(id,name) VALUES('` + tenant + `','` + tenant + `') ON CONFLICT DO NOTHING`,
		`INSERT INTO sites(id,tenant_id,name) VALUES('` + tenant + `-site','` + tenant + `','S') ON CONFLICT DO NOTHING`,
		`INSERT INTO gateways(id,tenant_id,site_id,serial,status) VALUES('` + tenant + `-gw','` + tenant + `','` + tenant + `-site','SER-` + tenant + `','active') ON CONFLICT DO NOTHING`,
		`DELETE FROM telemetry WHERE tenant_id='` + tenant + `'`,
		`DELETE FROM alerts WHERE tenant_id='` + tenant + `'`,
		`DELETE FROM points WHERE device_id IN (SELECT id FROM devices WHERE tenant_id='` + tenant + `')`,
		`DELETE FROM devices WHERE tenant_id='` + tenant + `'`,
		`INSERT INTO devices(id,tenant_id,gateway_id,profile,name) VALUES('` + tenant + `-dev','` + tenant + `','` + tenant + `-gw','modbus-tcp','Boiler')`,
		`INSERT INTO points(id,device_id,unit) VALUES('temp','` + tenant + `-dev','C')`,
		`INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,value,unit,schema_version)
		 SELECT '` + tenant + `-e'||g,'` + tenant + `','` + tenant + `-gw','` + tenant + `-dev','temp', now()-(g||' minutes')::interval, g, 'C', 1 FROM generate_series(1,10) g`,
		`INSERT INTO alerts(id,tenant_id,severity,message,status) VALUES('` + tenant + `-a1','` + tenant + `','critical','too hot','open'),('` + tenant + `-a2','` + tenant + `','info','ok','resolved')`,
	} {
		if _, err := s.st.Pool.Exec(context.Background(), q); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
}

func tool(t *testing.T, s *server, tenant, name string, args map[string]any) (any, error) {
	t.Helper()
	r := httptest.NewRequest("POST", "/mcp", nil)
	r = r.WithContext(context.WithValue(r.Context(), auth.CtxTenant, tenant))
	return s.callTool(r, name, args)
}

func TestToolsTenantIsolationAndAggregation(t *testing.T) {
	s := dbServer(t)
	seedMCP(t, s, "mcp-a")
	seedMCP(t, s, "mcp-b")

	out, err := tool(t, s, "mcp-a", "list_devices", nil)
	b, _ := json.Marshal(out)
	if err != nil || !strings.Contains(string(b), "mcp-a-dev") || strings.Contains(string(b), "mcp-b-dev") {
		t.Fatalf("list_devices leaks or misses: %v %s", err, b)
	}

	out, err = tool(t, s, "mcp-a", "list_alerts", map[string]any{"status": "open"})
	b, _ = json.Marshal(out)
	if err != nil || !strings.Contains(string(b), "too hot") || strings.Contains(string(b), "resolved") || strings.Contains(string(b), "mcp-b-a1") {
		t.Fatalf("list_alerts filter/isolation wrong: %v %s", err, b)
	}
	if _, err := tool(t, s, "mcp-a", "list_alerts", map[string]any{"status": "x'; drop"}); err == nil {
		t.Fatal("invalid status must be rejected")
	}

	// 10 readings 1..10 over the last 10 minutes, one 60-minute bucket or two.
	out, err = tool(t, s, "mcp-a", "aggregate_time_series", map[string]any{"device_id": "mcp-a-dev", "point_id": "temp", "hours": 1, "bucket_minutes": 60})
	rows, _ := out.([]map[string]any)
	if err != nil || len(rows) == 0 || len(rows) > 2 {
		t.Fatalf("aggregate rows: %v %v", err, out)
	}
	var total int
	var sum float64
	for _, r := range rows {
		total += r["count"].(int)
		sum += r["sum"].(float64)
	}
	if total != 10 || sum != 55 {
		t.Fatalf("aggregate count=%d sum=%v, want 10 and 55", total, sum)
	}
	// another tenant sees nothing for this device
	out, _ = tool(t, s, "mcp-b", "aggregate_time_series", map[string]any{"device_id": "mcp-a-dev", "point_id": "temp"})
	if rows, _ := out.([]map[string]any); len(rows) != 0 {
		t.Fatalf("aggregate leaks across tenants: %v", out)
	}
	if _, err := tool(t, s, "mcp-a", "aggregate_time_series", map[string]any{"device_id": "x", "point_id": "y", "hours": float64(168), "bucket_minutes": float64(1)}); err == nil {
		t.Fatal("bucket explosion must be rejected")
	}
}
