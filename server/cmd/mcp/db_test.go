package main

// DB-backed checks of the tool SQL: tenant isolation, bounds, aggregation.
// Skipped without TEST_DATABASE_URL (CI provides it).

import (
	"context"
	"encoding/json"
	"math"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

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

func toolAs(t *testing.T, s *server, tenant, role, name string, args map[string]any) (any, error) {
	t.Helper()
	r := httptest.NewRequest("POST", "/mcp", nil)
	ctx := context.WithValue(r.Context(), auth.CtxTenant, tenant)
	ctx = context.WithValue(ctx, auth.CtxRole, role)
	ctx = context.WithValue(ctx, auth.CtxUser, "mcp-user")
	return s.callTool(r.WithContext(ctx), name, args)
}

func TestDraftFlowGraphIsDraftOnlyAndGuarded(t *testing.T) {
	s := dbServer(t)
	seedMCP(t, s, "mcp-d1")
	seedMCP(t, s, "mcp-d2")
	ctx := context.Background()
	s.st.Pool.Exec(ctx, `DELETE FROM flows WHERE tenant_id IN ('mcp-d1','mcp-d2')`)
	s.st.Pool.Exec(ctx, `INSERT INTO notification_channels(id,tenant_id,type,target) VALUES('mcp-d1-ch','mcp-d1','slack','C1') ON CONFLICT DO NOTHING`)
	graph := func(ch string) map[string]any {
		var g map[string]any
		json.Unmarshal([]byte(`{"nodes":[{"id":"t","type":"trigger","device_id":"mcp-d1-dev","point_id":"temp","op":">","value":50},
		 {"id":"n","type":"notify","channel_id":"`+ch+`","message":"hot {value}"}],"edges":[{"from":"t","to":"n"}]}`), &g)
		return g
	}
	// catalogue and dry-run need no write role
	if out, err := toolAs(t, s, "mcp-d1", "viewer", "validate_flow_graph", map[string]any{"graph": graph("mcp-d1-ch"), "value": 60.0}); err != nil || !strings.Contains(mustJSON(out), `"hot 60"`) {
		t.Fatalf("validate: %v %s", err, mustJSON(out))
	}
	// viewers cannot draft
	if _, err := toolAs(t, s, "mcp-d1", "viewer", "draft_flow_graph", map[string]any{"name": "x", "graph": graph("mcp-d1-ch")}); err == nil {
		t.Fatal("viewer drafted a flow")
	}
	// another tenant's channel is refused
	if _, err := toolAs(t, s, "mcp-d2", "operator", "draft_flow_graph", map[string]any{"name": "x", "graph": graph("mcp-d1-ch")}); err == nil {
		t.Fatal("cross-tenant channel accepted")
	}
	// function nodes and unknown fields are refused
	fn := graph("mcp-d1-ch")
	fn["nodes"] = append(fn["nodes"].([]any), map[string]any{"id": "f", "type": "function", "code": "return msg"})
	if _, err := toolAs(t, s, "mcp-d1", "admin", "draft_flow_graph", map[string]any{"name": "x", "graph": fn}); err == nil {
		t.Fatal("function node accepted over MCP")
	}
	unk := graph("mcp-d1-ch")
	unk["shell"] = "rm -rf"
	if _, err := toolAs(t, s, "mcp-d1", "operator", "draft_flow_graph", map[string]any{"name": "x", "graph": unk}); err == nil {
		t.Fatal("unknown graph field accepted")
	}
	// the happy path stores an unpublished draft and audits it
	out, err := toolAs(t, s, "mcp-d1", "operator", "draft_flow_graph", map[string]any{"name": "from text", "graph": graph("mcp-d1-ch")})
	if err != nil {
		t.Fatal(err)
	}
	id := out.(map[string]any)["flow_id"].(string)
	var pub *string
	var enabled bool
	var status string
	s.st.Pool.QueryRow(ctx, `SELECT published_version_id, enabled FROM flows WHERE id=$1`, id).Scan(&pub, &enabled)
	s.st.Pool.QueryRow(ctx, `SELECT status FROM flow_versions WHERE flow_id=$1`, id).Scan(&status)
	if pub != nil || status != "draft" {
		t.Fatalf("not a pure draft: published=%v status=%s", pub, status)
	}
	var n int
	s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id='mcp-d1' AND action='flow.draft_mcp' AND target=$1`, id).Scan(&n)
	if n != 1 {
		t.Fatal("draft not audited")
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestAnalyticsToolsTenantScopedAndHonest(t *testing.T) {
	s := dbServer(t)
	seedMCP(t, s, "mcp-fa")
	seedMCP(t, s, "mcp-fb")
	ctx := context.Background()
	s.st.Pool.Exec(ctx, `DELETE FROM telemetry_rollup_hourly WHERE tenant_id IN ('mcp-fa','mcp-fb')`)
	end := time.Now().UTC().Truncate(time.Hour)
	for i := 1; i <= 24*14; i++ {
		b := end.Add(-time.Duration(i) * time.Hour)
		v := 50 + 10*math.Sin(2*math.Pi*float64(b.Hour())/24) + float64((i*7919)%11)/20 + 0.05*float64(24*14-i)
		s.st.Pool.Exec(ctx, `INSERT INTO telemetry_rollup_hourly(tenant_id,device_id,point_id,bucket,n,sum,min,max) VALUES('mcp-fa','mcp-fa-dev','temp',$1,1,$2,$2,$2)`, b, v)
	}
	out, err := tool(t, s, "mcp-fa", "forecast_time_series", map[string]any{"device_id": "mcp-fa-dev", "point_id": "temp", "horizon_hours": float64(6)})
	b, _ := json.Marshal(out)
	if err != nil || !strings.Contains(string(b), `"useful":true`) || !strings.Contains(string(b), `"label":"statistical"`) {
		t.Fatalf("forecast: %v %s", err, b)
	}
	if m := out.(map[string]any); len(m["forecast"].([]map[string]any)) != 6 {
		t.Fatalf("horizon not honoured: %s", b)
	}
	// another tenant must get "not enough data", never this tenant's numbers
	out, _ = tool(t, s, "mcp-fb", "forecast_time_series", map[string]any{"device_id": "mcp-fa-dev", "point_id": "temp"})
	if b, _ := json.Marshal(out); !strings.Contains(string(b), `"enough_data":false`) || strings.Contains(string(b), `"forecast":`) {
		t.Fatalf("forecast leaks across tenants: %s", b)
	}
	if _, err := tool(t, s, "mcp-fa", "forecast_time_series", map[string]any{"device_id": "x"}); err == nil {
		t.Fatal("missing point_id must be rejected")
	}
	out, err = tool(t, s, "mcp-fa", "related_signals", map[string]any{"device_id": "mcp-fa-dev", "point_id": "temp"})
	if b, _ := json.Marshal(out); err != nil || !strings.Contains(string(b), "correlated with, not caused by") {
		t.Fatalf("related: %v %s", err, b)
	}
}
