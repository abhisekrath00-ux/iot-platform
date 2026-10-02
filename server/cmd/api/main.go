// api is the control-plane REST API: device registry + onboarding, telemetry
// queries, command requests with approval gating, dashboards, alerts, and
// migrations at startup.
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/enroll"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/leader"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/notify"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/oidcstate"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/pki"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/redisx"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/report"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/respcache"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/retention"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/rules"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/search"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/secrets"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/tsstore"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type server struct {
	st      *store.Store
	secret  []byte
	es      *search.Client
	oidc    *auth.OIDCProvider
	states  oidcstate.Store // memory single-replica, Redis when REDIS_URL set
	cache   *respcache.Cache
	secrets *secrets.Store // nil or empty key = disabled (503)
	ts      tsstore.Store  // nil = Postgres; see internal/tsstore
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
	if k, err := secrets.KeyFromEnv(os.Getenv("SECRETS_KEY")); err != nil {
		log.Fatalf("%v", err)
	} else if k != nil {
		s.secrets = &secrets.Store{Pool: st.Pool, Key: k}
	}
	var redisCli *redisx.Client
	if ru := os.Getenv("REDIS_URL"); ru != "" {
		// redis://[:password@]host:port - shared OIDC state for multi-replica HA
		ru = strings.TrimPrefix(ru, "redis://")
		pass := ""
		if i := strings.Index(ru, "@"); i >= 0 {
			pass = strings.TrimPrefix(ru[:i], ":")
			ru = ru[i+1:]
		}
		s.states = oidcstate.NewRedis(ru, pass)
		log.Printf("oidc state: redis at %s", ru)
		redisCli = redisx.New(ru, pass)
	} else {
		s.states = oidcstate.NewMemory()
	}
	if iss := os.Getenv("OIDC_ISSUER"); iss != "" {
		p, err := auth.NewOIDCProvider(context.Background(), iss, os.Getenv("OIDC_CLIENT_ID"), os.Getenv("OIDC_CLIENT_SECRET"), os.Getenv("OIDC_REDIRECT_URL"))
		if err != nil {
			log.Fatalf("oidc: %v", err)
		}
		s.oidc = p
		log.Printf("oidc sso enabled for issuer %s", iss)
	}
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
	s.cache = newCache()
	if os.Getenv("CACHE_BACKEND") == "redis" { // opt-in: one cache and one invalidation for every replica
		if redisCli == nil {
			log.Fatalf("CACHE_BACKEND=redis needs REDIS_URL")
		}
		s.cache.UseShared(redisCli)
		log.Printf("response cache: shared via redis")
	}
	s.cached(api, "GET /v1/devices", s.listDevices)
	api.HandleFunc("POST /v1/devices", s.createDevice) // UI onboarding entry point
	api.HandleFunc("POST /v1/devices/bulk", s.bulkCreateDevices)
	api.HandleFunc("POST /v1/devices/{id}/tokens", s.createDeviceToken)
	api.HandleFunc("GET /v1/devices/{id}/tokens", s.listDeviceTokens)
	api.HandleFunc("DELETE /v1/devices/{id}/tokens/{tid}", s.revokeDeviceToken)
	s.cached(api, "GET /v1/telemetry/latest", s.latestTelemetry)
	api.HandleFunc("GET /v1/telemetry/count", s.countTelemetry)
	api.HandleFunc("GET /v1/telemetry/series", s.seriesTelemetry)
	api.HandleFunc("GET /v1/telemetry/rollup", s.rollupTelemetry)
	api.HandleFunc("GET /v1/telemetry/anomalies", s.anomaliesTelemetry)
	api.HandleFunc("GET /v1/telemetry/forecast", s.forecastTelemetry)
	api.HandleFunc("GET /v1/telemetry/related", s.relatedTelemetry)
	api.HandleFunc("POST /v1/telemetry/ingest", s.ingestHTTP)
	api.HandleFunc("POST /v1/commands", s.requestCommand)
	api.HandleFunc("POST /v1/commands/{id}/approve", s.approveCommand)
	api.HandleFunc("GET /v1/commands", s.listCommands)
	api.HandleFunc("GET /v1/secrets", s.listSecrets)
	api.HandleFunc("PUT /v1/secrets/{name}", s.putSecret)
	api.HandleFunc("DELETE /v1/secrets/{name}", s.deleteSecret)
	api.HandleFunc("PUT /v1/devices/{id}/tags", s.setDeviceTags)
	api.HandleFunc("PUT /v1/devices/{id}/asset", s.setDeviceAsset)
	api.HandleFunc("GET /v1/assets", s.listAssets)
	api.HandleFunc("GET /v1/direct-auth/policy", s.getDirectAuthPolicy)
	api.HandleFunc("PUT /v1/direct-auth/policy", s.putDirectAuthPolicy)
	api.HandleFunc("GET /v1/direct-devices", s.listDirectDevices)
	api.HandleFunc("POST /v1/direct-devices/password", s.createPasswordDevice)
	api.HandleFunc("POST /v1/direct-devices/{id}/rotate", s.rotatePasswordDevice)
	api.HandleFunc("POST /v1/direct-devices/{id}/revoke", s.revokeDirectDevice)
	api.HandleFunc("GET /v1/branding", s.getBranding)
	api.HandleFunc("PUT /v1/branding", s.putBranding)
	api.HandleFunc("GET /v1/kpis", s.listKPIs)
	api.HandleFunc("POST /v1/kpis", s.createKPI)
	api.HandleFunc("DELETE /v1/kpis/{id}", s.deleteKPI)
	api.HandleFunc("POST /v1/assets", s.createAsset)
	api.HandleFunc("DELETE /v1/assets/{id}", s.deleteAsset)
	api.HandleFunc("GET /v1/devices/{id}/health", s.deviceHealth)
	api.HandleFunc("GET /v1/api-keys", s.listAPIKeys)
	api.HandleFunc("POST /v1/api-keys", s.createAPIKey)
	api.HandleFunc("DELETE /v1/api-keys/{id}", s.revokeAPIKey)
	api.HandleFunc("GET /v1/alerts", s.listAlerts)
	api.HandleFunc("GET /v1/alerts/{id}", s.getAlert)
	api.HandleFunc("POST /v1/alerts/{id}/ack", s.ackAlert)
	api.HandleFunc("POST /v1/alerts/{id}/resolve", s.resolveAlert)
	api.HandleFunc("POST /v1/alerts/{id}/comments", s.commentAlert)
	api.HandleFunc("GET /v1/dashboards", s.listDashboards)
	s.cached(api, "GET /v1/fleet", s.fleetStatus)
	api.HandleFunc("GET /v1/audit", s.listAudit)
	api.HandleFunc("GET /v1/rules", s.listRules)
	api.HandleFunc("POST /v1/rules", s.createRule)
	api.HandleFunc("GET /v1/search", s.searchAll)
	api.HandleFunc("GET /v1/notifications/channels", s.listChannels)
	api.HandleFunc("POST /v1/notifications/channels", s.createChannel)
	api.HandleFunc("POST /v1/dashboards", s.saveDashboard)
	api.HandleFunc("PUT /v1/dashboards/{id}", s.updateDashboard)
	api.HandleFunc("DELETE /v1/dashboards/{id}", s.deleteDashboard)
	api.HandleFunc("POST /v1/enrollment/tokens", s.mintEnrollmentToken)
	api.HandleFunc("GET /v1/gateways/{id}/edge-config", s.gatewayEdgeConfig)
	s.cached(api, "GET /v1/profiles", s.listProfiles)
	api.HandleFunc("POST /v1/profiles", s.createProfile)
	api.HandleFunc("GET /v1/reports", s.listReports)
	api.HandleFunc("POST /v1/reports", s.createReport)
	api.HandleFunc("POST /v1/reports/{id}/run", s.runReport)
	api.HandleFunc("POST /v1/reports/preview", s.previewReport)
	api.HandleFunc("GET /v1/reports/{id}/download", s.downloadReport)
	s.cached(api, "GET /v1/points", s.listPoints)
	api.HandleFunc("GET /v1/export/telemetry.csv", s.exportTelemetryCSV)
	api.HandleFunc("GET /v1/retention", s.getRetention)
	api.HandleFunc("PUT /v1/retention", s.putRetention)
	api.HandleFunc("GET /v1/flows", s.listFlows)
	api.HandleFunc("GET /v1/flows/{id}/export", s.exportFlow)
	api.HandleFunc("POST /v1/flows/import", s.importFlow)
	api.HandleFunc("POST /v1/flows", s.createFlow)
	api.HandleFunc("GET /v1/flows/{id}/versions", s.listFlowVersions)
	api.HandleFunc("POST /v1/flows/{id}/draft", s.createFlowDraft)
	api.HandleFunc("POST /v1/flows/{id}/publish", s.publishFlow)
	api.HandleFunc("POST /v1/flows/{id}/rollback", s.rollbackFlow)
	api.HandleFunc("GET /v1/flows/{id}/versions/{version}", s.getFlowVersion)
	api.HandleFunc("PATCH /v1/flows/{id}", s.patchFlow)
	api.HandleFunc("DELETE /v1/flows/{id}", s.deleteFlow)
	api.HandleFunc("POST /v1/flows/{id}/duplicate", s.duplicateFlow)
	api.HandleFunc("POST /v1/flows/simulate", s.simulateFlow)
	api.HandleFunc("POST /v1/flows/graph/test", s.testFlowGraph)
	api.HandleFunc("POST /v1/flows/convert", s.convertFlow)
	api.HandleFunc("GET /v1/flows/{id}/export-nodered", s.exportNodeRED)
	api.HandleFunc("POST /v1/flows/import-nodered", s.importNodeRED)
	api.HandleFunc("GET /v1/features", s.getFeatures)
	api.HandleFunc("PUT /v1/features/{feature}", s.putFeature)
	api.HandleFunc("GET /v1/fleet/releases", s.listReleases)
	api.HandleFunc("POST /v1/fleet/releases", s.createRelease)
	api.HandleFunc("GET /v1/fleet/campaigns", s.listCampaigns)
	api.HandleFunc("POST /v1/fleet/campaigns", s.createCampaign)
	api.HandleFunc("POST /v1/fleet/campaigns/{id}/start", s.startCampaign)
	api.HandleFunc("POST /v1/fleet/campaigns/{id}/advance", s.advanceCampaign)
	api.HandleFunc("POST /v1/fleet/campaigns/{id}/pause", s.pauseCampaign)
	api.HandleFunc("POST /v1/fleet/campaigns/{id}/abort", s.abortCampaign)
	api.HandleFunc("POST /v1/fleet/campaigns/{id}/rollback", s.rollbackCampaign)
	api.HandleFunc("POST /v1/fleet/ack", s.ackAssignment)
	s.cached(api, "GET /v1/sites", s.listSites)
	api.HandleFunc("POST /v1/broker/acl/regenerate", s.regenerateBrokerACLHandler)
	api.HandleFunc("POST /v1/commissioning/sessions", s.createCommissionSession)
	api.HandleFunc("GET /v1/commissioning/sessions/{id}", s.getCommissionSession)
	api.HandleFunc("POST /v1/commissioning/sessions/{id}/profile", s.assignCommissionProfile)
	api.HandleFunc("POST /v1/commissioning/sessions/{id}/port-test", s.requestPortTest)
	api.HandleFunc("GET /v1/commissioning/sessions/{id}/preview", s.commissionPreview)

	// Bootstrap path: the gateway holds only its one-time claim code, no JWT yet.
	// Rate limited: 5/min per IP, burst 5 - brute-forcing 160-bit codes is
	// already infeasible, this is defense in depth.
	claimRL := auth.NewRateLimiter(5, 5)
	defer claimRL.Close()
	mux.Handle("POST /v1/enrollment/claim", claimRL.Middleware(http.HandlerFunc(s.claimEnrollment)))

	// SSO: unauthenticated by design; the callback issues the platform JWT.
	ssoRL := auth.NewRateLimiter(20, 10)
	defer ssoRL.Close()
	mux.Handle("GET /auth/oidc/login", ssoRL.Middleware(http.HandlerFunc(s.oidcLogin)))
	mux.Handle("GET /auth/oidc/callback", ssoRL.Middleware(http.HandlerFunc(s.oidcCallback)))

	// API keys are unattended credentials: cap each key (per replica). Override with API_KEY_RPM; 0 disables.
	rpm := 600
	if v, err := strconv.Atoi(os.Getenv("API_KEY_RPM")); err == nil && v >= 0 {
		rpm = v
	}
	if rpm > 0 {
		auth.SetKeyLimiter(auth.NewLimiter(rpm, rpm/5+1))
	}
	mux.HandleFunc("POST /v1/lorawan/uplink", s.lorawanUplink) // device token auth, like /v1/device/ingest
	mux.HandleFunc("POST /v1/device/ingest", s.deviceIngest)   // device token auth, outside the session middleware
	mux.Handle("/v1/", auth.Middleware(s.secret, s.resolveAPIKey)(s.invalidateOnWrite(api)))

	// Only one replica runs the scheduler at a time (Postgres advisory lock).
	go leader.Run(ctx, st.Pool, leaderReportScheduler, "report-scheduler", 10*time.Second, s.reportScheduler)
	// Timed flows (inject nodes): same single-replica rule, checked every 30s.
	go leader.Run(ctx, st.Pool, leaderFlowScheduler, "flow-scheduler", 10*time.Second, func(c context.Context) {
		fn := notify.FromEnv()
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			flow.RunScheduled(c, st.Pool, fn)
			select {
			case <-c.Done():
				return
			case <-t.C:
			}
		}
	})
	// KPI band rules are evaluated once a minute by one replica.
	go leader.Run(ctx, st.Pool, leaderKPIRules, "kpi-rules", 10*time.Second, func(c context.Context) {
		fn := notify.FromEnv()
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		tick := 0
		for {
			select {
			case <-c.Done():
				return
			case <-t.C:
				rules.EvaluateKPIs(c, st.Pool, fn)
				if tick++; tick%15 == 0 {
					rules.EvaluateForecasts(c, st.Pool, fn)
				}
			}
		}
	})
	// Hourly rollups always; raw-data purge only when RAW_RETENTION_DAYS is set (default: keep everything).
	retDays, _ := strconv.Atoi(os.Getenv("RAW_RETENTION_DAYS"))
	go leader.Run(ctx, st.Pool, leaderRetention, "retention", 30*time.Second, retention.Job(st.Pool, retDays, time.Hour))
	srv := &http.Server{Addr: ":" + envOr("API_PORT", "8000"), Handler: auth.SecurityHeaders(mux), ReadHeaderTimeout: 10 * time.Second}
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

// migrateAdvisoryLockID serializes schema migrations across every process
// that boots against the same database (api replicas, and any sibling
// containers). Without it, two fresh starters race on CREATE TABLE IF NOT
// EXISTS and collide on the table's implicit pg_type row.
const migrateAdvisoryLockID int64 = 0x4845584D4F4E // "HEXMON"

// leaderReportScheduler is the advisory lock key for the report scheduler.
const leaderReportScheduler int64 = 0x4845584D4F4F

// leaderFlowScheduler is the advisory lock key for timed (inject) flows.
const leaderFlowScheduler int64 = 0x4845584D4F51

// leaderKPIRules is the advisory lock key for KPI band rule evaluation.
const leaderKPIRules int64 = 0x4845584D4F52

// leaderRetention is the advisory lock key for the rollup/retention job.
const leaderRetention int64 = 0x4845584D4F50

func migrate(ctx context.Context, st *store.Store) error {
	conn, err := st.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrateAdvisoryLockID); err != nil {
		return err
	}
	defer func() {
		// the boot context may already be cancelled; unlock on a fresh one
		conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", migrateAdvisoryLockID) //nolint:errcheck
	}()
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
		if _, err := conn.Exec(ctx, string(b)); err != nil {
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
	// Optional fleet filters: ?q= (substring of name/id/profile), ?tag= (exact), ?gateway_id=
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	tag := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("tag")))
	gw := r.URL.Query().Get("gateway_id")
	asset := r.URL.Query().Get("asset_id") // matches the asset and everything below it
	rows, err := s.st.Pool.Query(r.Context(),
		`WITH RECURSIVE sub AS (
		   SELECT id FROM assets WHERE tenant_id=$1 AND id=NULLIF($5,'')
		   UNION ALL SELECT a.id FROM assets a JOIN sub ON a.parent_id=sub.id WHERE a.tenant_id=$1
		 )
		 SELECT id, profile, name, gateway_id, created_at, tags, asset_id FROM devices
		 WHERE tenant_id=$1
		   AND ($5='' OR asset_id IN (SELECT id FROM sub))
		   AND ($2='' OR name ILIKE '%'||$2||'%' OR id ILIKE '%'||$2||'%' OR profile ILIKE '%'||$2||'%')
		   AND ($3='' OR $3 = ANY(tags))
		   AND ($4='' OR gateway_id=$4)
		 ORDER BY created_at DESC LIMIT 1000`, auth.Tenant(r), q, tag, gw, asset)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, profile, name, gw string
		var created time.Time
		var tags []string
		var assetID *string
		rows.Scan(&id, &profile, &name, &gw, &created, &tags, &assetID)
		out = append(out, map[string]any{"id": id, "profile": profile, "name": name, "gateway_id": gw, "created_at": created, "tags": tags, "asset_id": assetID})
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

// countTelemetry reports rows ingested for a device since a timestamp.
// Read-only; used by the load-test harness and ops checks.
func (s *server) countTelemetry(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dev := q.Get("device_id")
	if dev == "" || len(dev) > 128 {
		http.Error(w, "device_id required", 400)
		return
	}
	since := time.Now().Add(-time.Hour)
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, "since must be RFC3339", 400)
			return
		}
		since = t
	}
	var n int
	if err := s.st.Pool.QueryRow(r.Context(),
		`SELECT count(*) FROM telemetry WHERE tenant_id=$1 AND device_id=$2 AND received_at > $3`,
		auth.Tenant(r), dev, since).Scan(&n); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]any{"device_id": dev, "since": since, "count": n})
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
	// Approval is a human decision: an API key (unattended automation) may
	// request a command but can never approve one.
	if auth.ViaKey(r) {
		http.Error(w, "approval requires an interactive user session", 403)
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
	status := s.dispatchCommand(r, id)
	writeJSON(w, 200, map[string]any{"request_id": id, "status": status})
}

// commandEnvelope is the strict wire format gateways receive (docs/architecture.md).
type commandEnvelope struct {
	RequestID     string          `json:"request_id"`
	Target        string          `json:"target"`
	Action        string          `json:"action"`
	Parameters    json.RawMessage `json:"parameters"`
	ApprovedBy    string          `json:"approved_by"`
	IssuedAt      time.Time       `json:"issued_at"`
	ExpiresAt     time.Time       `json:"expires_at"`
	PolicyVersion string          `json:"policy_version"`
}

// commandTopic is tenant- and gateway-scoped so broker ACLs can pin a gateway to its own topic.
func commandTopic(tenant, gateway string) string { return "t/" + tenant + "/g/" + gateway + "/cmd" }

// dispatchCommand publishes an approved command. It is never retained (a
// reconnecting gateway must not replay stale actuation); the envelope carries
// expires_at and the gateway must drop expired commands. Status becomes 'sent'
// only after the broker accepted the publish, else 'failed'. 'sent' is not
// 'acked': nothing consumes the topic until the edge executor exists.
func (s *server) dispatchCommand(r *http.Request, id string) string {
	var env commandEnvelope
	var gw, params string
	err := s.st.Pool.QueryRow(r.Context(),
		`SELECT request_id, gateway_id, device_id, action, parameters::text, approved_by, issued_at, expires_at, policy_version
		 FROM commands WHERE request_id=$1 AND tenant_id=$2`, id, auth.Tenant(r)).
		Scan(&env.RequestID, &gw, &env.Target, &env.Action, &params, &env.ApprovedBy, &env.IssuedAt, &env.ExpiresAt, &env.PolicyVersion)
	env.Parameters = json.RawMessage(params)
	if err == nil {
		var b []byte
		if b, err = json.Marshal(env); err == nil {
			err = s.publishMQTTRetained(commandTopic(auth.Tenant(r), gw), b, false)
		}
	}
	next := "sent"
	if err != nil {
		next = "failed"
	}
	s.st.Pool.Exec(r.Context(), `UPDATE commands SET status=$1 WHERE request_id=$2 AND status='approved'`, next, id)
	s.audit(r, "command."+next, id, map[string]any{"topic_gateway": gw})
	return next
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
	status := r.URL.Query().Get("status")
	if status != "" && status != "open" && status != "acknowledged" && status != "resolved" {
		http.Error(w, "status must be open, acknowledged or resolved", 400)
		return
	}
	asset := r.URL.Query().Get("asset_id") // alerts on devices under this asset
	rows, err := s.st.Pool.Query(r.Context(),
		`WITH RECURSIVE sub AS (
		   SELECT id FROM assets WHERE tenant_id=$1 AND id=NULLIF($3,'')
		   UNION ALL SELECT a.id FROM assets a JOIN sub ON a.parent_id=sub.id WHERE a.tenant_id=$1
		 )
		 SELECT id, severity, message, status, created_at, acknowledged_by, resolved_by FROM alerts
		 WHERE tenant_id=$1 AND ($2 = '' OR status=$2)
		   AND ($3='' OR device_id IN (SELECT d.id FROM devices d WHERE d.tenant_id=$1 AND d.asset_id IN (SELECT id FROM sub)))
		 ORDER BY created_at DESC LIMIT 100`, auth.Tenant(r), status, asset)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, sev, msg, stt string
		var created time.Time
		var ackBy, resBy *string
		rows.Scan(&id, &sev, &msg, &stt, &created, &ackBy, &resBy)
		out = append(out, map[string]any{"id": id, "severity": sev, "message": msg, "status": stt, "created_at": created,
			"acknowledged_by": ackBy, "resolved_by": resBy})
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
	if !requireRole(w, r, "admin", "operator") {
		return
	}
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
	s.audit(r, "dashboard.create", id, map[string]any{"name": in.Name})
	writeJSON(w, 201, map[string]any{"id": id})
}

// updateDashboard replaces name/layout of an existing dashboard. Admin/operator only.
func (s *server) updateDashboard(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	id := r.PathValue("id")
	var in struct {
		Name   string          `json:"name"`
		Layout json.RawMessage `json:"layout"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Name == "" {
		http.Error(w, "name required", 400)
		return
	}
	res, err := s.st.Pool.Exec(r.Context(),
		`UPDATE dashboards SET name=$1, layout=$2 WHERE id=$3 AND tenant_id=$4`,
		in.Name, in.Layout, id, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if res.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "dashboard.update", id, map[string]any{"name": in.Name})
	writeJSON(w, 200, map[string]any{"id": id})
}

// deleteDashboard removes a dashboard. Admin/operator only.
func (s *server) deleteDashboard(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	id := r.PathValue("id")
	res, err := s.st.Pool.Exec(r.Context(),
		`DELETE FROM dashboards WHERE id=$1 AND tenant_id=$2`, id, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if res.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "dashboard.delete", id, nil)
	writeJSON(w, 200, map[string]any{"deleted": id})
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
		Kind     string `json:"kind"` // edge (default) | direct (network device, telemetry-only)
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.SiteID == "" || in.Serial == "" {
		http.Error(w, "site_id and serial required", 400)
		return
	}
	if in.Kind == "" {
		in.Kind = "edge"
	}
	if in.Kind != "edge" && in.Kind != "direct" {
		http.Error(w, "kind must be edge or direct", 400)
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
		`INSERT INTO gateways(id,tenant_id,site_id,serial,status,kind)
		 SELECT $1,$2,$3,$4,'pending',$5 WHERE EXISTS (SELECT 1 FROM sites WHERE id=$3 AND tenant_id=$2)`,
		gwID, tenant, in.SiteID, in.Serial, in.Kind); err != nil {
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
	s.audit(r, "enrollment.mint", gwID, map[string]any{"serial": in.Serial, "site_id": in.SiteID, "kind": in.Kind})
	writeJSON(w, 201, map[string]any{
		"gateway_id": gwID, "kind": in.Kind, "claim_code": code, "expires_at": expires,
		"enroll_string": enrollString(envOr("API_PUBLIC_URL", "http://localhost:8000"), code, in.Serial),
		"note":          "show this code to the installer once; it is not stored",
	})
}

// claimEnrollment redeems a one-time claim code + serial for a gateway
// credential. Single-use, expiry-checked, constant-time code comparison.
// Unauthenticated by design (bootstrap); the claim code is the secret.
func (s *server) claimEnrollment(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ClaimCode string `json:"claim_code"`
		Serial    string `json:"serial"`
		CSRPEM    string `json:"csr_pem"` // optional: gateway requests a client certificate
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
	resp := map[string]any{"gateway_id": gwID, "ingest_token": signed, "status": "active"}
	if in.CSRPEM != "" {
		// mTLS issuance: sign the gateway CSR with the deployment CA. CN is
		// bound to the claimed serial, and the fingerprint lands on the
		// gateway row for revocation and audit.
		caCert, certErr1 := os.ReadFile(os.Getenv("MTLS_CA_CERT"))
		caKey, certErr2 := os.ReadFile(os.Getenv("MTLS_CA_KEY"))
		if certErr1 != nil || certErr2 != nil {
			http.Error(w, "mtls CA not configured on server", 500)
			return
		}
		certPEM, fp, err := pki.SignCSR(caCert, caKey, []byte(in.CSRPEM), in.Serial, 825)
		if err != nil {
			http.Error(w, "csr rejected: "+err.Error(), 400)
			return
		}
		if _, err := s.st.Pool.Exec(r.Context(),
			`UPDATE gateways SET cert_fingerprint=$1 WHERE id=$2`, fp, gwID); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		resp["client_cert_pem"] = string(certPEM)
		resp["ca_cert_pem"] = string(caCert)
		resp["cert_fingerprint"] = fp
	}
	s.regenerateBrokerACL(r.Context())
	writeJSON(w, 200, resp)
}

// --- OIDC SSO ---

// oidcLogin redirects to the provider with a fresh state + nonce pair.
func (s *server) oidcLogin(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		http.NotFound(w, r)
		return
	}
	state, err := auth.RandomToken()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	nonce, err := auth.RandomToken()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := s.states.Put(state, nonce, 10*time.Minute); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, s.oidc.AuthURL(state, nonce), http.StatusFound)
}

// oidcCallback verifies the code exchange, maps email to a platform user,
// and issues the platform JWT. Unknown emails are rejected: SSO authenticates
// identity, it does not create tenants or users.
func (s *server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	state, code := q.Get("state"), q.Get("code")
	nonce, ok := s.states.Take(state) // single use, TTL enforced by the store
	if !ok || code == "" {
		http.Error(w, "invalid or expired state", 403)
		return
	}
	ident, err := s.oidc.Exchange(r.Context(), code, nonce)
	if err != nil {
		http.Error(w, "oidc exchange failed", 403)
		return
	}
	var userID, tenantID, role string
	err = s.st.Pool.QueryRow(r.Context(),
		`SELECT id, tenant_id, role FROM users WHERE lower(email)=lower($1)`, ident.Email).
		Scan(&userID, &tenantID, &role)
	if err != nil {
		http.Error(w, "no platform user for this account", 403)
		return
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{
		TenantID: tenantID, Role: role,
		RegisteredClaims: jwt.RegisteredClaims{Subject: userID, ExpiresAt: jwt.NewNumericDate(time.Now().Add(12 * time.Hour))},
	})
	signed, err := tok.SignedString(s.secret)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit(r, "auth.sso_login", userID, map[string]any{"email": ident.Email})
	writeJSON(w, 200, map[string]any{"token": signed, "user_id": userID, "tenant_id": tenantID, "role": role})
}

// --- device profiles (multi-sensor onboarding) ---

var profilePointOK = regexp.MustCompile(`^[a-z0-9_-]{1,128}$`)

func validProfilePoints(pts []map[string]any) error {
	if len(pts) == 0 || len(pts) > 64 {
		return fmt.Errorf("1-64 points required")
	}
	for _, p := range pts {
		id, _ := p["id"].(string)
		if !profilePointOK.MatchString(id) {
			return fmt.Errorf("point id %q invalid", id)
		}
		if _, ok := p["register"].(float64); !ok {
			return fmt.Errorf("point %s: register required", id)
		}
		if fn, ok := p["func"].(float64); ok && (fn < 1 || fn > 4) {
			return fmt.Errorf("point %s: func must be 1-4", id)
		}
		switch t, _ := p["type"].(string); t {
		case "", "u16", "i16", "u32", "i32", "f32", "bool":
		default:
			return fmt.Errorf("point %s: unknown type %q", id, t)
		}
		switch w, _ := p["word_order"].(string); w {
		case "", "abcd", "badc", "cdab", "dcba":
		default:
			return fmt.Errorf("point %s: unknown word_order %q", id, w)
		}
	}
	return nil
}

func (s *server) listProfiles(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT id, name, driver_profile, points, created_at FROM device_profiles WHERE tenant_id=$1 ORDER BY created_at DESC`,
		auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, drv string
		var pts []byte
		var created time.Time
		rows.Scan(&id, &name, &drv, &pts, &created)
		out = append(out, map[string]any{"id": id, "name": name, "driver_profile": drv, "points": json.RawMessage(pts), "created_at": created})
	}
	writeJSON(w, 200, out)
}

func (s *server) createProfile(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		Name          string           `json:"name"`
		DriverProfile string           `json:"driver_profile"`
		Points        []map[string]any `json:"points"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 128 || strings.ContainsAny(name, "<>\x00") {
		http.Error(w, "name invalid", 400)
		return
	}
	if _, ok := driverKinds[in.DriverProfile]; !ok {
		http.Error(w, "driver_profile must be a supported edge driver", 400)
		return
	}
	if err := validateProfilePointsFor(in.DriverProfile, in.Points); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	pts, _ := json.Marshal(in.Points)
	id := uuid.NewString()
	_, err := s.st.Pool.Exec(r.Context(),
		`INSERT INTO device_profiles(id,tenant_id,name,driver_profile,points,created_by) VALUES($1,$2,$3,$4,$5,$6)`,
		id, auth.Tenant(r), name, in.DriverProfile, pts, auth.User(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit(r, "profile.create", id, map[string]any{"name": name, "driver_profile": in.DriverProfile})
	writeJSON(w, 201, map[string]any{"id": id})
}

// --- report builder ---

func (s *server) listReports(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT id, name, definition, schedule_cron, channel_id, last_run_at, created_at
		 FROM reports WHERE tenant_id=$1 ORDER BY created_at DESC`, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name string
		var def []byte
		var cron, channelID *string
		var lastRun *time.Time
		var createdAt time.Time
		rows.Scan(&id, &name, &def, &cron, &channelID, &lastRun, &createdAt)
		out = append(out, map[string]any{"id": id, "name": name, "definition": json.RawMessage(def),
			"schedule_cron": cron, "channel_id": channelID, "last_run_at": lastRun, "created_at": createdAt})
	}
	writeJSON(w, 200, out)
}

func (s *server) createReport(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		Name         string            `json:"name"`
		Definition   report.Definition `json:"definition"`
		ScheduleCron string            `json:"schedule_cron"`
		ChannelID    string            `json:"channel_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 128 || strings.ContainsAny(name, "<>\x00") {
		http.Error(w, "name invalid", 400)
		return
	}
	if err := report.Validate(in.Definition); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var cron *string
	if in.ScheduleCron != "" {
		if _, err := report.NextRun(in.ScheduleCron, time.Now()); err != nil {
			http.Error(w, "schedule_cron: "+err.Error(), 400)
			return
		}
		cron = &in.ScheduleCron
	}
	var channelID *string
	if in.ChannelID != "" {
		channelID = &in.ChannelID
	}
	def, _ := json.Marshal(in.Definition)
	id := uuid.NewString()
	_, err := s.st.Pool.Exec(r.Context(),
		`INSERT INTO reports(id,tenant_id,name,definition,schedule_cron,channel_id,created_by)
		 SELECT $1,$2,$3,$4,$5,$6,$7 WHERE $6::text IS NULL OR EXISTS
		   (SELECT 1 FROM notification_channels WHERE id=$6 AND tenant_id=$2)`,
		id, auth.Tenant(r), name, def, cron, channelID, auth.User(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit(r, "report.create", id, map[string]any{"name": name})
	writeJSON(w, 201, map[string]any{"id": id})
}

// runReport aggregates the report's metrics over its window, renders a
// standalone HTML document, delivers it to the configured channel, and
// records the run. Also called by the scheduler for cron-due reports.
func (s *server) runReport(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	id := r.PathValue("id")
	if err := s.executeReport(r.Context(), id, auth.Tenant(r)); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]any{"report_id": id, "status": "ok"})
}

func (s *server) executeReport(ctx context.Context, id, tenant string) error {
	var name string
	var defBytes []byte
	var channelID *string
	err := s.st.Pool.QueryRow(ctx,
		`SELECT name, definition, channel_id FROM reports WHERE id=$1 AND tenant_id=$2`, id, tenant).
		Scan(&name, &defBytes, &channelID)
	if err != nil {
		return fmt.Errorf("report lookup: %w", err)
	}
	var def report.Definition
	if err := json.Unmarshal(defBytes, &def); err != nil {
		return err
	}
	runID := uuid.NewString()
	if _, err := s.st.Pool.Exec(ctx,
		`INSERT INTO report_runs(id,report_id) VALUES($1,$2)`, runID, id); err != nil {
		return err
	}
	fail := func(err error) error {
		s.st.Pool.Exec(ctx, `UPDATE report_runs SET finished_at=now(), status='failed', error=$2 WHERE id=$1`, runID, err.Error())
		return err
	}
	series, total, err := s.buildSeries(ctx, tenant, def)
	if err != nil {
		return fail(err)
	}
	htmlDoc := report.Render(name, def, series, time.Now())
	if channelID != nil {
		var ctype, target string
		var enabled bool
		err := s.st.Pool.QueryRow(ctx,
			`SELECT type, target, enabled FROM notification_channels WHERE id=$1 AND tenant_id=$2`,
			*channelID, tenant).Scan(&ctype, &target, &enabled)
		if err != nil || !enabled {
			return fail(fmt.Errorf("channel unavailable"))
		}
		n := notify.FromEnv()
		switch ctype {
		case "email":
			if err := n.Email(ctx, []string{target}, "Report: "+name, htmlDoc); err != nil {
				return fail(err)
			}
		case "slack":
			if err := n.Slack(ctx, target, "Report ready: "+name+" ("+fmt.Sprint(total)+" rows)"); err != nil {
				return fail(err)
			}
		case "webhook":
			if err := n.Webhook(ctx, target, "report.ready", map[string]any{"report": name, "rows": total}); err != nil {
				return fail(err)
			}
		case "kafka":
			if err := n.Kafka(ctx, target, "report.ready", map[string]any{"report": name, "rows": total}); err != nil {
				return fail(err)
			}
		case "amqp":
			if err := n.AMQP(ctx, target, "report.ready", map[string]any{"report": name, "rows": total}); err != nil {
				return fail(err)
			}
		}
	}
	if _, err := s.st.Pool.Exec(ctx,
		`UPDATE report_runs SET finished_at=now(), status='ok', row_count=$2 WHERE id=$1`, runID, total); err != nil {
		return err
	}
	_, err = s.st.Pool.Exec(ctx, `UPDATE reports SET last_run_at=now() WHERE id=$1`, id)
	return err
}

// buildSeries aggregates telemetry for every metric in a definition, reading
// through the tsstore seam.
func (s *server) buildSeries(ctx context.Context, tenant string, def report.Definition) (map[report.Metric][]report.Bucket, int, error) {
	if report.BucketExpr(def.GroupBy) == "" {
		return nil, 0, report.ErrBadGroupBy
	}
	store := s.ts
	if store == nil {
		store = tsstore.NewPostgres(s.st.Pool)
	}
	series := map[report.Metric][]report.Bucket{}
	total := 0
	for _, m := range def.Metrics {
		bs, err := store.Aggregate(ctx, tsstore.SeriesQuery{Tenant: tenant, DeviceID: m.DeviceID, PointID: m.PointID, WindowHours: def.WindowHours, GroupBy: def.GroupBy})
		if err != nil {
			return nil, 0, err
		}
		if len(bs) > 0 {
			series[m] = bs
		}
		total += len(bs)
	}
	return series, total, nil
}

// previewReport renders a definition without storing or delivering it.
func (s *server) previewReport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name       string            `json:"name"`
		Definition report.Definition `json:"definition"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if err := report.Validate(in.Definition); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "Preview"
	}
	series, total, err := s.buildSeries(r.Context(), auth.Tenant(r), in.Definition)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]any{"html": report.Render(name, in.Definition, series, time.Now()), "rows": total})
}

// downloadReport returns the stored report's current data as CSV or HTML.
func (s *server) downloadReport(w http.ResponseWriter, r *http.Request) {
	var name string
	var defBytes []byte
	if err := s.st.Pool.QueryRow(r.Context(),
		`SELECT name, definition FROM reports WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), auth.Tenant(r)).
		Scan(&name, &defBytes); err != nil {
		http.Error(w, "report not found", 404)
		return
	}
	var def report.Definition
	if err := json.Unmarshal(defBytes, &def); err != nil {
		http.Error(w, "report definition corrupt", 500)
		return
	}
	def, perr := report.ApplyParams(def, r.URL.Query().Get)
	if perr != nil {
		http.Error(w, perr.Error(), 400)
		return
	}
	series, _, err := s.buildSeries(r.Context(), auth.Tenant(r), def)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	safe := strings.Map(func(c rune) rune {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			return c
		}
		return '_'
	}, name)
	s.audit(r, "report.download", r.PathValue("id"), map[string]any{"format": r.URL.Query().Get("format")})
	if r.URL.Query().Get("format") == "html" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+safe+`.html"`)
		fmt.Fprint(w, report.Render(name, def, series, time.Now()))
		return
	}
	switch r.URL.Query().Get("format") {
	case "pdf":
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="`+safe+`.pdf"`)
		w.Write(report.RenderPDF(name, def, series, time.Now()))
		return
	case "xlsx":
		b, err := report.RenderXLSX(def, series)
		if err != nil {
			http.Error(w, "xlsx build failed", 500)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition", `attachment; filename="`+safe+`.xlsx"`)
		w.Write(b)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+safe+`.csv"`)
	fmt.Fprint(w, report.RenderCSV(def, series))
}

// listPoints feeds the report/dashboard pickers: every device with its points.
func (s *server) listPoints(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT d.id, d.name, p.id, COALESCE(p.unit,'') FROM devices d JOIN points p ON p.device_id=d.id
		 WHERE d.tenant_id=$1 ORDER BY d.name, p.id LIMIT 5000`, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var did, dname, pid, unit string
		rows.Scan(&did, &dname, &pid, &unit)
		out = append(out, map[string]any{"device_id": did, "device_name": dname, "point_id": pid, "unit": unit})
	}
	writeJSON(w, 200, out)
}

// exportTelemetryCSV streams raw samples for SCADA historians, BI tools and
// spreadsheets. Bounded (hours <= 720, 200k rows) so one call cannot hurt ingest.
func (s *server) exportTelemetryCSV(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dev, pt := q.Get("device_id"), q.Get("point_id")
	hours, _ := strconv.Atoi(q.Get("hours"))
	if hours < 1 || hours > 720 {
		hours = 24
	}
	if dev == "" || len(dev) > 128 || len(pt) > 128 {
		http.Error(w, "device_id required", 400)
		return
	}
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT observed_at, point_id, value, unit, quality FROM telemetry
		 WHERE tenant_id=$1 AND device_id=$2 AND ($3='' OR point_id=$3)
		   AND observed_at > now() - ($4 || ' hours')::interval
		 ORDER BY observed_at LIMIT 200000`, auth.Tenant(r), dev, pt, fmt.Sprint(hours))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="telemetry.csv"`)
	s.audit(r, "export.telemetry", dev, map[string]any{"point_id": pt, "hours": hours})
	cw := csv.NewWriter(w)
	cw.Write([]string{"observed_at", "point_id", "value", "unit", "quality"})
	for rows.Next() {
		var t time.Time
		var p, u, ql string
		var v float64
		rows.Scan(&t, &p, &v, &u, &ql)
		cw.Write([]string{t.UTC().Format(time.RFC3339Nano), report.CSVSafe(p), strconv.FormatFloat(v, 'g', -1, 64), report.CSVSafe(u), report.CSVSafe(ql)})
	}
	cw.Flush()
}

// reportScheduler fires cron-due reports once per minute. Multi-replica
// deployments need a lease here (see docs/deployment.md HA notes).
func (s *server) reportScheduler(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			rows, err := s.st.Pool.Query(ctx,
				`SELECT id, tenant_id, schedule_cron, coalesce(last_run_at, created_at)
				 FROM reports WHERE schedule_cron IS NOT NULL`)
			if err != nil {
				continue
			}
			type due struct{ id, tenant string }
			var dues []due
			for rows.Next() {
				var id, tenant, cron string
				var anchor time.Time
				rows.Scan(&id, &tenant, &cron, &anchor)
				next, err := report.NextRun(cron, anchor)
				if err == nil && !next.After(now) {
					dues = append(dues, due{id, tenant})
				}
			}
			rows.Close()
			for _, d := range dues {
				if err := s.executeReport(ctx, d.id, d.tenant); err != nil {
					log.Printf("report %s: %v", d.id, err)
				}
			}
		}
	}
}

// --- flow builder ---

func (s *server) listFlows(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT f.id, f.name, f.definition, f.enabled, f.created_at, v.version,
		   (SELECT max(version) FROM flow_versions x WHERE x.flow_id=f.id)
		 FROM flows f LEFT JOIN flow_versions v ON v.id = f.published_version_id
		 WHERE f.tenant_id=$1 ORDER BY f.created_at DESC`,
		auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name string
		var def []byte
		var enabled bool
		var created time.Time
		var ver, latest *int
		rows.Scan(&id, &name, &def, &enabled, &created, &ver, &latest)
		out = append(out, map[string]any{"id": id, "name": name, "definition": json.RawMessage(def),
			"enabled": enabled, "created_at": created, "published_version": ver, "latest_version": latest})
	}
	writeJSON(w, 200, out)
}

func (s *server) createFlow(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		Name       string          `json:"name"`
		Definition flow.Definition `json:"definition"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 128 || strings.ContainsAny(name, "<>\x00") {
		http.Error(w, "name invalid", 400)
		return
	}
	if err := flow.Validate(in.Definition); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if !s.gateFunctionNodes(w, r, in.Definition) {
		return
	}
	// referenced channels must belong to this tenant
	if !s.checkFlowChannels(r, in.Definition) {
		http.Error(w, "notify channel not found for this tenant", 400)
		return
	}
	def, _ := json.Marshal(in.Definition)
	id := uuid.NewString()
	verID := uuid.NewString()
	tx, err := s.st.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(),
		`INSERT INTO flows(id,tenant_id,name,definition,created_by) VALUES($1,$2,$3,$4,$5)`,
		id, auth.Tenant(r), name, def, auth.User(r)); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// ?draft=1 saves an unpublished draft (the graph editor uses this so a person
	// publishes deliberately); the default keeps the previous publish-on-create.
	if r.URL.Query().Get("draft") == "1" {
		if _, err := tx.Exec(r.Context(),
			`INSERT INTO flow_versions(id,flow_id,tenant_id,version,definition,status,created_by)
			 VALUES($1,$2,$3,1,$4,'draft',$5)`,
			verID, id, auth.Tenant(r), def, auth.User(r)); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	} else {
		if _, err := tx.Exec(r.Context(),
			`INSERT INTO flow_versions(id,flow_id,tenant_id,version,definition,status,created_by,published_at)
			 VALUES($1,$2,$3,1,$4,'published',$5,now())`,
			verID, id, auth.Tenant(r), def, auth.User(r)); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if _, err := tx.Exec(r.Context(),
			`UPDATE flows SET published_version_id=$1 WHERE id=$2`, verID, id); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit(r, "flow.create", id, map[string]any{"name": name})
	writeJSON(w, 201, map[string]any{"id": id, "version": 1})
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
	asset := r.URL.Query().Get("asset_id") // rules that name a device under this asset
	rows, err := s.st.Pool.Query(r.Context(),
		`WITH RECURSIVE sub AS (
		   SELECT id FROM assets WHERE tenant_id=$1 AND id=NULLIF($2,'')
		   UNION ALL SELECT a.id FROM assets a JOIN sub ON a.parent_id=sub.id WHERE a.tenant_id=$1
		 )
		 SELECT id, name, definition, version, enabled FROM rules
		 WHERE tenant_id=$1
		   AND ($2='' OR definition->>'device_id' IN (SELECT d.id FROM devices d WHERE d.tenant_id=$1 AND d.asset_id IN (SELECT id FROM sub)))
		 ORDER BY created_at DESC`, auth.Tenant(r), asset)
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
	// kinds: threshold (point_id, op >|<, threshold), sigma (point_id, sigma, window_minutes, direction) or kpi_band (kpi_id, min and/or max)
	var def rules.Definition
	if err := json.Unmarshal(in.Definition, &def); err != nil {
		http.Error(w, "invalid rule definition", 400)
		return
	}
	if err := def.Validate(); err != nil {
		http.Error(w, "invalid rule definition: "+err.Error(), 400)
		return
	}
	if def.Kind == "kpi_band" {
		var one int
		if s.st.Pool.QueryRow(r.Context(), `SELECT 1 FROM kpis WHERE id=$1 AND tenant_id=$2`, def.KPIID, auth.Tenant(r)).Scan(&one) != nil {
			http.Error(w, "unknown kpi", 404)
			return
		}
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
		(in.Type != "email" && in.Type != "slack" && in.Type != "webhook" && in.Type != "kafka" && in.Type != "amqp") {
		http.Error(w, "type (email|slack|webhook|kafka|amqp) and target required", 400)
		return
	}
	if in.Type == "webhook" {
		if err := notify.ValidateWebhookURL(in.Target); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	}
	if in.Type == "kafka" {
		if _, _, err := notify.ParseKafkaTarget(in.Target); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	}
	if in.Type == "amqp" {
		if _, _, _, _, err := notify.ParseAMQPTarget(in.Target); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
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
