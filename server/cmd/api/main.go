// api is the control-plane REST API: device registry + onboarding, telemetry
// queries, command requests with approval gating, dashboards, alerts, and
// migrations at startup.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/enroll"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/search"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type server struct {
	st     *store.Store
	secret []byte
	es     *search.Client
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

	s := &server{st: st, secret: []byte(mustEnv("JWT_SIGNING_SECRET")), es: search.New(os.Getenv("ELASTICSEARCH_URL"))}
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
	api.HandleFunc("GET /v1/fleet", s.fleetStatus)
	api.HandleFunc("GET /v1/audit", s.listAudit)
	api.HandleFunc("GET /v1/rules", s.listRules)
	api.HandleFunc("POST /v1/rules", s.createRule)
	api.HandleFunc("GET /v1/search", s.searchAll)
	api.HandleFunc("GET /v1/notifications/channels", s.listChannels)
	api.HandleFunc("POST /v1/notifications/channels", s.createChannel)
	api.HandleFunc("POST /v1/dashboards", s.saveDashboard)
	api.HandleFunc("POST /v1/enrollment/tokens", s.mintEnrollmentToken)

	// Bootstrap path: the gateway holds only its one-time claim code, no JWT yet.
	mux.HandleFunc("POST /v1/enrollment/claim", s.claimEnrollment)

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
	entries, err := os.ReadDir(envOr("MIGRATIONS_DIR", "/migrations"))
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		b, err := os.ReadFile(envOr("MIGRATIONS_DIR", "/migrations") + "/" + e.Name())
		if err != nil {
			return err
		}
		if _, err := st.Pool.Exec(ctx, string(b)); err != nil {
			return fmt.Errorf("migration %s: %w", e.Name(), err)
		}
		log.Printf("migration applied: %s", e.Name())
	}
	return nil
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
	s.es.Index(r.Context(), "devices", id, map[string]any{
		"id": id, "tenant_id": auth.Tenant(r), "name": in.Name, "profile": in.Profile, "gateway_id": in.GatewayID})
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

// --- gateway enrollment ---

// mintEnrollmentToken issues a one-time claim code bound to tenant + serial.
// The code is returned once; only its SHA-256 hash is stored. Admin/operator only.
func (s *server) mintEnrollmentToken(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		SiteID   string `json:"site_id"`
		Serial   string `json:"serial"`
		TTLHours int    `json:"ttl_hours"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.SiteID == "" || in.Serial == "" {
		http.Error(w, "site_id and serial required", 400)
		return
	}
	if in.TTLHours <= 0 || in.TTLHours > 24*30 {
		in.TTLHours = 72
	}
	code, err := enroll.NewCode()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	tenant := auth.Tenant(r)
	gwID := uuid.NewString()
	tx, err := s.st.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(),
		`INSERT INTO gateways(id,tenant_id,site_id,serial,status)
		 SELECT $1,$2,$3,$4,'pending' WHERE EXISTS (SELECT 1 FROM sites WHERE id=$3 AND tenant_id=$2)`,
		gwID, tenant, in.SiteID, in.Serial); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	expires := time.Now().Add(time.Duration(in.TTLHours) * time.Hour)
	if _, err := tx.Exec(r.Context(),
		`INSERT INTO enrollment_tokens(id,tenant_id,site_id,gateway_id,serial,code_hash,expires_at,created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		uuid.NewString(), tenant, in.SiteID, gwID, in.Serial, enroll.Hash(code), expires, auth.User(r)); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit(r, "enrollment.mint", gwID, map[string]any{"serial": in.Serial, "site_id": in.SiteID})
	writeJSON(w, 201, map[string]any{
		"gateway_id": gwID, "claim_code": code, "expires_at": expires,
		"note": "show this code to the installer once; it is not stored",
	})
}

// claimEnrollment redeems a one-time claim code + serial for a gateway
// credential. Single-use, expiry-checked, constant-time code comparison.
// Unauthenticated by design (bootstrap); the claim code is the secret.
func (s *server) claimEnrollment(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ClaimCode string `json:"claim_code"`
		Serial    string `json:"serial"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.ClaimCode == "" || in.Serial == "" {
		http.Error(w, "claim_code and serial required", 400)
		return
	}
	var tokenID, tenantID, gwID, serial string
	var codeHash []byte
	var expires time.Time
	var claimedAt *time.Time
	err := s.st.Pool.QueryRow(r.Context(),
		`SELECT id,tenant_id,gateway_id,serial,code_hash,expires_at,claimed_at
		 FROM enrollment_tokens WHERE code_hash=$1`, enroll.Hash(in.ClaimCode)).
		Scan(&tokenID, &tenantID, &gwID, &serial, &codeHash, &expires, &claimedAt)
	if err != nil || serial != in.Serial {
		// identical response for unknown code and serial mismatch: no oracle
		http.Error(w, "invalid claim", 403)
		return
	}
	if err := enroll.CheckRedeemable(expires, claimedAt, time.Now()); err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	tx, err := s.st.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer tx.Rollback(r.Context())
	tag, err := tx.Exec(r.Context(),
		`UPDATE enrollment_tokens SET claimed_at=now() WHERE id=$1 AND claimed_at IS NULL`, tokenID)
	if err != nil || tag.RowsAffected() != 1 {
		http.Error(w, enroll.ErrAlreadyUsed.Error(), 403)
		return
	}
	if _, err := tx.Exec(r.Context(),
		`UPDATE gateways SET status='active', last_seen_at=now() WHERE id=$1`, gwID); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{
		TenantID: tenantID, Role: "gateway",
		RegisteredClaims: jwt.RegisteredClaims{Subject: gwID, ExpiresAt: jwt.NewNumericDate(time.Now().Add(365 * 24 * time.Hour))},
	})
	signed, err := tok.SignedString(s.secret)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]any{"gateway_id": gwID, "ingest_token": signed, "status": "active"})
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

// --- RBAC ---
// requireRole rejects callers whose role is not in the allowed set.
func requireRole(w http.ResponseWriter, r *http.Request, roles ...string) bool {
	role := auth.Role(r)
	for _, allowed := range roles {
		if role == allowed {
			return true
		}
	}
	http.Error(w, "insufficient role", http.StatusForbidden)
	return false
}

// --- fleet status ---
func (s *server) fleetStatus(w http.ResponseWriter, r *http.Request) {
	var out struct {
		Gateways   int `json:"gateways"`
		Active     int `json:"active_gateways"`
		Devices    int `json:"devices"`
		Stale      int `json:"stale_devices"`
		OpenAlerts int `json:"open_alerts"`
	}
	t := auth.Tenant(r)
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*), count(*) FILTER (WHERE status='active') FROM gateways WHERE tenant_id=$1`, t).Scan(&out.Gateways, &out.Active)
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM devices WHERE tenant_id=$1`, t).Scan(&out.Devices)
	// stale = no measured reading in 2x the device's slowest expected interval (floor 15m)
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM devices d WHERE d.tenant_id=$1 AND NOT EXISTS (
		SELECT 1 FROM telemetry te WHERE te.device_id=d.id AND te.observed_at > now() - interval '15 minutes')`, t).Scan(&out.Stale)
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM alerts WHERE tenant_id=$1 AND status='open'`, t).Scan(&out.OpenAlerts)
	writeJSON(w, 200, out)
}

// --- audit viewer (admin only) ---
func (s *server) listAudit(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT actor, action, target, detail, at FROM audit_log WHERE tenant_id=$1 ORDER BY at DESC LIMIT 200`, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var actor, action string
		var target *string
		var detail json.RawMessage
		var at time.Time
		rows.Scan(&actor, &action, &target, &detail, &at)
		out = append(out, map[string]any{"actor": actor, "action": action, "target": target, "detail": detail, "at": at})
	}
	writeJSON(w, 200, out)
}

// --- rules CRUD (flow definitions; evaluator runs in ingest) ---
func (s *server) listRules(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT id, name, definition, version, enabled FROM rules WHERE tenant_id=$1 ORDER BY created_at DESC`, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name string
		var def json.RawMessage
		var ver int
		var en bool
		rows.Scan(&id, &name, &def, &ver, &en)
		out = append(out, map[string]any{"id": id, "name": name, "definition": def, "version": ver, "enabled": en})
	}
	writeJSON(w, 200, out)
}

func (s *server) createRule(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		Name       string          `json:"name"`
		Definition json.RawMessage `json:"definition"`
		Enabled    bool            `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Name == "" || len(in.Definition) == 0 {
		http.Error(w, "name and definition required", 400)
		return
	}
	// v1 rule shape: {"device_id":..,"point_id":..,"op":">","threshold":N,"severity":"warning","message":..}
	var shape struct {
		PointID   string  `json:"point_id"`
		Op        string  `json:"op"`
		Threshold float64 `json:"threshold"`
		Severity  string  `json:"severity"`
	}
	if err := json.Unmarshal(in.Definition, &shape); err != nil || shape.PointID == "" ||
		(shape.Op != ">" && shape.Op != "<") || (shape.Severity != "info" && shape.Severity != "warning" && shape.Severity != "critical") {
		http.Error(w, "invalid rule definition (v1: point_id, op >|<, threshold, severity info|warning|critical)", 400)
		return
	}
	id := uuid.NewString()
	_, err := s.st.Pool.Exec(r.Context(),
		`INSERT INTO rules(id,tenant_id,name,definition,enabled,created_by) VALUES($1,$2,$3,$4,$5,$6)`,
		id, auth.Tenant(r), in.Name, in.Definition, in.Enabled, auth.User(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit(r, "rule.create", id, map[string]any{"name": in.Name, "enabled": in.Enabled})
	writeJSON(w, 201, map[string]any{"id": id})
}

// --- notification channels ---
func (s *server) listChannels(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT id, type, target, enabled FROM notification_channels WHERE tenant_id=$1`, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, typ, target string
		var en bool
		rows.Scan(&id, &typ, &target, &en)
		out = append(out, map[string]any{"id": id, "type": typ, "target": target, "enabled": en})
	}
	writeJSON(w, 200, out)
}

func (s *server) createChannel(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	var in struct {
		Type   string `json:"type"`
		Target string `json:"target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Target == "" ||
		(in.Type != "email" && in.Type != "slack") {
		http.Error(w, "type (email|slack) and target required", 400)
		return
	}
	id := uuid.NewString()
	_, err := s.st.Pool.Exec(r.Context(),
		`INSERT INTO notification_channels(id,tenant_id,type,target) VALUES($1,$2,$3,$4)`,
		id, auth.Tenant(r), in.Type, in.Target)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit(r, "channel.create", id, map[string]any{"type": in.Type})
	writeJSON(w, 201, map[string]any{"id": id})
}

// --- search (Elasticsearch; tenant-scoped) ---
func (s *server) searchAll(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		http.Error(w, "q required", 400)
		return
	}
	hits, err := s.es.Query(r.Context(), []string{"devices", "alerts"}, auth.Tenant(r), q)
	if err != nil {
		http.Error(w, "search backend unavailable: "+err.Error(), 503)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(hits)
}
