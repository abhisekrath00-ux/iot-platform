// api is the control-plane REST API: device registry + onboarding, telemetry
// queries, command requests with approval gating, dashboards, alerts, and
// migrations at startup.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
	"github.com/google/uuid"
)

type server struct {
	st     *store.Store
	secret []byte
}

func main() {
	seed := flag.Bool("seed-demo", false, "insert demo tenant/site/gateway/devices then exit")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	st, err := store.Connect(ctx, mustEnv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	if err := migrate(ctx, st); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	if *seed {
		if err := seedDemo(ctx, st); err != nil {
			log.Fatalf("seed: %v", err)
		}
		log.Printf("demo data seeded")
		return
	}

	s := &server{st: st, secret: []byte(mustEnv("JWT_SIGNING_SECRET"))}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := st.Pool.Ping(r.Context()); err != nil {
			http.Error(w, "db down", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	})

	// Authenticated API (all tenant-scoped).
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/devices", s.listDevices)
	api.HandleFunc("POST /v1/devices", s.createDevice) // UI onboarding entry point
	api.HandleFunc("GET /v1/telemetry/latest", s.latestTelemetry)
	api.HandleFunc("GET /v1/telemetry/series", s.seriesTelemetry)
	api.HandleFunc("POST /v1/commands", s.requestCommand)
	api.HandleFunc("POST /v1/commands/{id}/approve", s.approveCommand)
	api.HandleFunc("GET /v1/commands", s.listCommands)
	api.HandleFunc("GET /v1/alerts", s.listAlerts)
	api.HandleFunc("GET /v1/dashboards", s.listDashboards)
	api.HandleFunc("POST /v1/dashboards", s.saveDashboard)

	mux.Handle("/v1/", auth.Middleware(s.secret)(api))

	srv := &http.Server{Addr: ":" + envOr("API_PORT", "8000"), Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("api listening on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()
	<-ctx.Done()
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(shutCtx)
}

func migrate(ctx context.Context, st *store.Store) error {
	b, err := os.ReadFile(envOr("MIGRATIONS_DIR", "/migrations") + "/0001_init.sql")
	if err != nil {
		return err
	}
	_, err = st.Pool.Exec(ctx, string(b))
	return err
}

func seedDemo(ctx context.Context, st *store.Store) error {
	_, err := st.Pool.Exec(ctx, `
		INSERT INTO tenants(id,name) VALUES('demo','Demo Tenant') ON CONFLICT DO NOTHING;
		INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('demo-admin','demo','admin@demo.local','Demo Admin','admin') ON CONFLICT DO NOTHING;
		INSERT INTO sites(id,tenant_id,name) VALUES('demo-site','demo','Demo Site') ON CONFLICT DO NOTHING;
		INSERT INTO gateways(id,tenant_id,site_id,serial,status) VALUES('demo-gw','demo','demo-site','AXON-DEMO-1','active') ON CONFLICT DO NOTHING;
		INSERT INTO devices(id,tenant_id,gateway_id,profile,name) VALUES
		  ('meter-1','demo','demo-gw','modbus-energy-meter','Main energy meter'),
		  ('door-1','demo','demo-gw','door-contact','Server room door') ON CONFLICT DO NOTHING;
		INSERT INTO points(id,device_id,unit,min_value,max_value) VALUES
		  ('kwh','meter-1','kWh',0,1000000),('voltage','meter-1','V',0,500),('current','meter-1','A',0,200),
		  ('state','door-1','bool',0,1) ON CONFLICT DO NOTHING;`)
	return err
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// --- devices / onboarding ---

func (s *server) listDevices(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT id, profile, name, gateway_id, created_at FROM devices WHERE tenant_id=$1 ORDER BY created_at DESC`, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, profile, name, gw string
		var created time.Time
		rows.Scan(&id, &profile, &name, &gw, &created)
		out = append(out, map[string]any{"id": id, "profile": profile, "name": name, "gateway_id": gw, "created_at": created})
	}
	writeJSON(w, 200, out)
}

func (s *server) createDevice(w http.ResponseWriter, r *http.Request) {
	var in struct {
		GatewayID string         `json:"gateway_id"`
		Profile   string         `json:"profile"`
		Name      string         `json:"name"`
		Config    map[string]any `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.GatewayID == "" || in.Profile == "" || in.Name == "" {
		http.Error(w, "gateway_id, profile, name required", 400)
		return
	}
	id := uuid.NewString()
	cfg, _ := json.Marshal(in.Config)
	_, err := s.st.Pool.Exec(r.Context(),
		`INSERT INTO devices(id,tenant_id,gateway_id,profile,name,config)
		 SELECT $1, $2, $3, $4, $5, $6 WHERE EXISTS
		   (SELECT 1 FROM gateways WHERE id=$3 AND tenant_id=$2)`,
		id, auth.Tenant(r), in.GatewayID, in.Profile, in.Name, cfg)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit(r, "device.create", id, map[string]any{"profile": in.Profile, "name": in.Name})
	writeJSON(w, 201, map[string]any{"id": id})
}

// --- telemetry ---

func (s *server) latestTelemetry(w http.ResponseWriter, r *http.Request) {
	device := r.URL.Query().Get("device_id")
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT DISTINCT ON (point_id) point_id, value, unit, quality, observed_at
		 FROM telemetry WHERE tenant_id=$1 AND device_id=$2
		 ORDER BY point_id, observed_at DESC`, auth.Tenant(r), device)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var p, u, q string
		var v float64
		var t time.Time
		rows.Scan(&p, &v, &u, &q, &t)
		out = append(out, map[string]any{"point_id": p, "value": v, "unit": u, "quality": q, "observed_at": t})
	}
	writeJSON(w, 200, out)
}

func (s *server) seriesTelemetry(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT observed_at, value, quality FROM telemetry
		 WHERE tenant_id=$1 AND device_id=$2 AND point_id=$3
		   AND observed_at > now() - interval '24 hours'
		 ORDER BY observed_at LIMIT 5000`,
		auth.Tenant(r), q.Get("device_id"), q.Get("point_id"))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var t time.Time
		var v float64
		var qual string
		rows.Scan(&t, &v, &qual)
		out = append(out, map[string]any{"t": t, "v": v, "quality": qual})
	}
	writeJSON(w, 200, out)
}

// --- commands (control path, approval-gated) ---

func (s *server) requestCommand(w http.ResponseWriter, r *http.Request) {
	var in struct {
		GatewayID string         `json:"gateway_id"`
		DeviceID  string         `json:"device_id"`
		Action    string         `json:"action"`
		Params    map[string]any `json:"parameters"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Action == "" || in.DeviceID == "" {
		http.Error(w, "device_id and action required", 400)
		return
	}
	id := uuid.NewString()
	params, _ := json.Marshal(in.Params)
	_, err := s.st.Pool.Exec(r.Context(),
		`INSERT INTO commands(request_id,tenant_id,gateway_id,device_id,action,parameters,requested_by)
		 VALUES($1,$2,$3,$4,$5,$6,$7)`,
		id, auth.Tenant(r), in.GatewayID, in.DeviceID, in.Action, params, auth.User(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit(r, "command.request", id, map[string]any{"action": in.Action, "device": in.DeviceID})
	writeJSON(w, 201, map[string]any{"request_id": id, "status": "pending_approval"})
}

func (s *server) approveCommand(w http.ResponseWriter, r *http.Request) {
	if auth.Role(r) != "admin" && auth.Role(r) != "operator" {
		http.Error(w, "approver role required", 403)
		return
	}
	id := r.PathValue("id")
	// Approver must differ from requester (four-eyes on actuation).
	tag, err := s.st.Pool.Exec(r.Context(),
		`UPDATE commands SET status='approved', approved_by=$1, issued_at=now(), expires_at=now()+interval '5 minutes'
		 WHERE request_id=$2 AND tenant_id=$3 AND status='pending_approval' AND requested_by<>$1`,
		auth.User(r), id, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "not found, already decided, or self-approval", 409)
		return
	}
	s.audit(r, "command.approve", id, nil)
	// TODO: publish to gateway cmd topic with the command envelope.
	writeJSON(w, 200, map[string]any{"request_id": id, "status": "approved"})
}

func (s *server) listCommands(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT request_id, device_id, action, status, requested_by, approved_by, created_at
		 FROM commands WHERE tenant_id=$1 ORDER BY created_at DESC LIMIT 100`, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, dev, act, stt, by string
		var appr *string
		var created time.Time
		rows.Scan(&id, &dev, &act, &stt, &by, &appr, &created)
		out = append(out, map[string]any{"request_id": id, "device_id": dev, "action": act, "status": stt,
			"requested_by": by, "approved_by": appr, "created_at": created})
	}
	writeJSON(w, 200, out)
}

// --- alerts & dashboards ---

func (s *server) listAlerts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT id, severity, message, status, created_at FROM alerts WHERE tenant_id=$1 ORDER BY created_at DESC LIMIT 100`, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, sev, msg, stt string
		var created time.Time
		rows.Scan(&id, &sev, &msg, &stt, &created)
		out = append(out, map[string]any{"id": id, "severity": sev, "message": msg, "status": stt, "created_at": created})
	}
	writeJSON(w, 200, out)
}

func (s *server) listDashboards(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT id, name, layout FROM dashboards WHERE tenant_id=$1 ORDER BY created_at DESC`, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name string
		var layout json.RawMessage
		rows.Scan(&id, &name, &layout)
		out = append(out, map[string]any{"id": id, "name": name, "layout": layout})
	}
	writeJSON(w, 200, out)
}

func (s *server) saveDashboard(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name   string          `json:"name"`
		Layout json.RawMessage `json:"layout"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Name == "" {
		http.Error(w, "name required", 400)
		return
	}
	id := uuid.NewString()
	_, err := s.st.Pool.Exec(r.Context(),
		`INSERT INTO dashboards(id,tenant_id,name,layout,created_by) VALUES($1,$2,$3,$4,$5)`,
		id, auth.Tenant(r), in.Name, in.Layout, auth.User(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *server) audit(r *http.Request, action, target string, detail map[string]any) {
	d, _ := json.Marshal(detail)
	if _, err := s.st.Pool.Exec(r.Context(),
		`INSERT INTO audit_log(tenant_id,actor,action,target,detail) VALUES($1,$2,$3,$4,$5)`,
		auth.Tenant(r), auth.User(r), action, target, d); err != nil {
		log.Printf("audit write failed: %v", err)
	}
}

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
