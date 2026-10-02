// mcp is the read-only MCP gateway: JSON-RPC 2.0 over HTTP exposing
// allowlisted tools (list_sites, get_device_health, query_time_series,
// explain_alert). Every call requires the user's delegated JWT (tenant +
// role). The only write is draft_flow_graph, which stores an UNPUBLISHED flow
// draft; there is no actuation or publish path here by design (see
// docs/security.md).
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
)

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResp struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

type server struct{ st *store.Store }

var tools = []map[string]any{
	{"name": "list_sites", "description": "List sites in the caller's tenant with gateway and device counts.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
	{"name": "get_device_health", "description": "Latest readings and freshness for one device.",
		"inputSchema": map[string]any{"type": "object", "required": []string{"device_id"},
			"properties": map[string]any{"device_id": map[string]any{"type": "string"}}}},
	{"name": "query_time_series", "description": "Recent readings for a device point (max 24h, 500 points).",
		"inputSchema": map[string]any{"type": "object", "required": []string{"device_id", "point_id"},
			"properties": map[string]any{
				"device_id": map[string]any{"type": "string"},
				"point_id":  map[string]any{"type": "string"},
				"hours":     map[string]any{"type": "integer", "maximum": 24}}}},
	{"name": "list_devices", "description": "List devices in the caller's tenant with profile and gateway (max 200).",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
	{"name": "list_alerts", "description": "Recent alerts, newest first (max 100). Optional status filter: open, acknowledged, resolved.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"status": map[string]any{"type": "string", "enum": []string{"open", "acknowledged", "resolved"}},
			"limit":  map[string]any{"type": "integer", "maximum": 100}}}},
	{"name": "aggregate_time_series", "description": "Bucketed avg/min/max/sum for a device point (max 7 days, 500 buckets).",
		"inputSchema": map[string]any{"type": "object", "required": []string{"device_id", "point_id"},
			"properties": map[string]any{
				"device_id":      map[string]any{"type": "string"},
				"point_id":       map[string]any{"type": "string"},
				"hours":          map[string]any{"type": "integer", "maximum": 168},
				"bucket_minutes": map[string]any{"type": "integer", "minimum": 1, "maximum": 1440}}}},
	{"name": "explain_alert", "description": "Alert details plus recent readings around it.",
		"inputSchema": map[string]any{"type": "object", "required": []string{"alert_id"},
			"properties": map[string]any{"alert_id": map[string]any{"type": "string"}}}},
}

func init() { tools = append(tools, flowTools...) }

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	st, err := store.Connect(ctx, mustEnv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	s := &server{st: st}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.Handle("POST /mcp", auth.Middleware([]byte(mustEnv("JWT_SIGNING_SECRET")))(http.HandlerFunc(s.handle)))

	srv := &http.Server{Addr: ":" + envOr("MCP_PORT", "8100"), Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("mcp listening on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()
	<-ctx.Done()
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(c)
}

func (s *server) handle(w http.ResponseWriter, r *http.Request) {
	var req rpcReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json-rpc", 400)
		return
	}
	resp := rpcResp{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": "2025-06-18",
			"serverInfo":      map[string]any{"name": "hexmon-iot-mcp", "version": "0.1.0"},
			"capabilities":    map[string]any{"tools": map[string]any{}},
		}
	case "tools/list":
		resp.Result = map[string]any{"tools": tools}
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		json.Unmarshal(req.Params, &p)
		out, err := s.callTool(r, p.Name, p.Arguments)
		if err != nil {
			resp.Error = map[string]any{"code": -32000, "message": err.Error()}
		} else {
			b, _ := json.Marshal(out)
			resp.Result = map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}}
		}
	case "notifications/initialized":
		json.NewEncoder(w).Encode(resp) // ack-style empty result
		return
	default:
		resp.Error = map[string]any{"code": -32601, "message": "method not found"}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *server) callTool(r *http.Request, name string, args map[string]any) (any, error) {
	ctx := r.Context()
	t := auth.Tenant(r)
	if out, handled, err := s.flowTool(r, name, args); handled {
		return out, err
	}
	if out, handled, err := s.analyticsTool(r, name, args); handled {
		return out, err
	}
	switch name {
	case "list_sites":
		rows, err := s.st.Pool.Query(ctx, `
			SELECT s.id, s.name,
			  (SELECT count(*) FROM gateways g WHERE g.site_id=s.id),
			  (SELECT count(*) FROM devices d JOIN gateways g ON g.id=d.gateway_id WHERE g.site_id=s.id)
			FROM sites s WHERE s.tenant_id=$1 ORDER BY s.name`, t)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id, name string
			var gw, dev int
			rows.Scan(&id, &name, &gw, &dev)
			out = append(out, map[string]any{"id": id, "name": name, "gateways": gw, "devices": dev})
		}
		return out, nil

	case "get_device_health":
		id, _ := args["device_id"].(string)
		rows, err := s.st.Pool.Query(ctx, `
			SELECT DISTINCT ON (point_id) point_id, value, unit, quality, observed_at
			FROM telemetry WHERE tenant_id=$1 AND device_id=$2
			ORDER BY point_id, observed_at DESC`, t, id)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var p, u, q string
			var v float64
			var at time.Time
			rows.Scan(&p, &v, &u, &q, &at)
			out = append(out, map[string]any{
				"point_id": p, "value": v, "unit": u, "quality": q,
				"observed_at": at, "age_seconds": int(time.Since(at).Seconds()),
			})
		}
		return map[string]any{"device_id": id, "points": out}, nil

	case "query_time_series":
		id, _ := args["device_id"].(string)
		pt, _ := args["point_id"].(string)
		hours := 1.0
		if h, ok := args["hours"].(float64); ok && h > 0 && h <= 24 {
			hours = h
		}
		rows, err := s.st.Pool.Query(ctx, `
			SELECT observed_at, value, quality FROM telemetry
			WHERE tenant_id=$1 AND device_id=$2 AND point_id=$3
			  AND observed_at > now() - ($4 || ' hours')::interval
			ORDER BY observed_at LIMIT 500`, t, id, pt, hours)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var at time.Time
			var v float64
			var q string
			rows.Scan(&at, &v, &q)
			out = append(out, map[string]any{"t": at, "v": v, "quality": q})
		}
		return out, nil

	case "list_devices":
		rows, err := s.st.Pool.Query(ctx, `
			SELECT id, name, profile, gateway_id FROM devices WHERE tenant_id=$1 ORDER BY name LIMIT 200`, t)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id, name, profile, gw string
			rows.Scan(&id, &name, &profile, &gw)
			out = append(out, map[string]any{"id": id, "name": name, "profile": profile, "gateway_id": gw})
		}
		return out, nil

	case "list_alerts":
		status, _ := args["status"].(string)
		if status != "" && status != "open" && status != "acknowledged" && status != "resolved" {
			return nil, &toolError{"invalid status"}
		}
		limit := clampInt(args["limit"], 25, 1, 100)
		rows, err := s.st.Pool.Query(ctx, `
			SELECT id, severity, message, status, created_at FROM alerts
			WHERE tenant_id=$1 AND ($2 = '' OR status=$2) ORDER BY created_at DESC LIMIT $3`, t, status, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id, sev, msg, st string
			var at time.Time
			rows.Scan(&id, &sev, &msg, &st, &at)
			out = append(out, map[string]any{"id": id, "severity": sev, "message": msg, "status": st, "created_at": at})
		}
		return out, nil

	case "aggregate_time_series":
		id, _ := args["device_id"].(string)
		pt, _ := args["point_id"].(string)
		hours := clampInt(args["hours"], 24, 1, 168)
		bucket := clampInt(args["bucket_minutes"], 60, 1, 1440)
		if hours*60/bucket > 500 {
			return nil, &toolError{"too many buckets: raise bucket_minutes or lower hours (max 500 buckets)"}
		}
		rows, err := s.st.Pool.Query(ctx, `
			SELECT to_timestamp(floor(extract(epoch FROM observed_at) / ($5::int*60)) * ($5::int*60)) AS b,
			       avg(value), min(value), max(value), sum(value), count(*)
			FROM telemetry
			WHERE tenant_id=$1 AND device_id=$2 AND point_id=$3
			  AND observed_at > now() - make_interval(hours => $4::int)
			GROUP BY b ORDER BY b LIMIT 500`, t, id, pt, hours, bucket)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var at time.Time
			var avg, mn, mx, sum float64
			var n int
			rows.Scan(&at, &avg, &mn, &mx, &sum, &n)
			out = append(out, map[string]any{"t": at, "avg": avg, "min": mn, "max": mx, "sum": sum, "count": n})
		}
		return out, nil

	case "explain_alert":
		id, _ := args["alert_id"].(string)
		var sev, msg, status string
		var at time.Time
		err := s.st.Pool.QueryRow(ctx, `
			SELECT severity, message, status, created_at FROM alerts WHERE tenant_id=$1 AND id=$2`,
			t, id).Scan(&sev, &msg, &status, &at)
		if err != nil {
			return nil, err
		}
		return map[string]any{"id": id, "severity": sev, "message": msg, "status": status, "created_at": at}, nil
	}
	return nil, &toolError{"unknown tool: " + name}
}

type toolError struct{ s string }

func (e *toolError) Error() string { return e.s }

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("missing env %s", k)
	}
	return v
}
func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// clampInt reads a JSON number argument with a default and hard bounds.
func clampInt(v any, def, lo, hi int) int {
	f, ok := v.(float64)
	if !ok {
		return def
	}
	n := int(f)
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}
