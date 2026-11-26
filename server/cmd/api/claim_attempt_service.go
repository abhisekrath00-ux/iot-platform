package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/enroll"
	"io"
	"net/http"
	"strings"
	"time"
)

// registerCandidateEnrollment is the single product router composition point.
// main supplies a zero config: no environment switch can enable this candidate.
// Only isolated acceptance callers supply explicit synthetic trust and schema.
func registerCandidateEnrollment(ctx context.Context, mux *http.ServeMux, cfg enroll.CandidateServiceConfig) (func(), error) {
	service, e := enroll.NewCandidateService(ctx, cfg)
	if e != nil {
		return nil, e
	}
	mux.Handle("/candidate/", service.Handler)
	return service.Close, nil
}

// Shared registrations preserve product middleware and route semantics in acceptance.
func registerEnrollmentMint(api *http.ServeMux, s *server) {
	api.HandleFunc("POST /v1/enrollment/tokens", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-HexThings-Commissioning") == "1" {
			s.mintCommissioning(w, r)
			return
		}
		s.mintEnrollmentToken(w, r)
	})
	api.HandleFunc("GET /v1/enrollment/commissioning", s.commissioningReadiness)
	api.HandleFunc("GET /v1/enrollment/commissioning/receipt", s.commissioningReceipt)
}
func registerAuthenticatedAPI(mux, api *http.ServeMux, s *server) {
	s.inner = s.invalidateOnWrite(s.activeUser(s.customerScope(api)))
	mux.Handle("/v1/", auth.Middleware(s.secret, s.resolveAPIKey)(s.inner))
}

// Private composition freezes public trust. No environment reader/key generator.
type commissioningSetup struct {
	Origin, TransportCA, DeviceCA string
	ResponseKey                   ed25519.PublicKey
	Config                        enroll.CandidateServiceConfig
}

func configureCommissioning(s *server, origin, transport string, cfg enroll.CandidateServiceConfig) error {
	normalized, e := normalizeEndpointURL(origin)
	if e != nil || normalized != origin || !strings.HasPrefix(origin, "https://") || isLoopbackURL(origin) || !cfg.Enabled || cfg.Pool != s.st.Pool || cfg.Issuer.Lifetime != 45*time.Minute || !bytes.Equal(cfg.Issuer.TokenKey, s.secret) {
		return fmt.Errorf("explicit commissioning origin and compatible authority required")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(transport)) {
		return fmt.Errorf("transport roots required")
	}
	if e = cfg.Issuer.Validate(); e != nil {
		return e
	}
	if b, _ := pem.Decode(cfg.Issuer.CA); b == nil {
		return fmt.Errorf("device roots required")
	}
	service, e := enroll.NewCandidateService(context.Background(), cfg)
	if e != nil {
		return e
	}
	service.Close()
	s.commissioning = &commissioningSetup{origin, transport, string(cfg.Issuer.CA), append(ed25519.PublicKey(nil), cfg.Issuer.ResponseKey.Public().(ed25519.PublicKey)...), cfg}
	return nil
}
func (s *server) commissioningEligible(w http.ResponseWriter, r *http.Request) bool {
	if auth.ViaKey(r) {
		http.Error(w, "interactive session required", 403)
		return false
	}
	if !requireRole(w, r, "admin", "operator") {
		return false
	}
	var live bool
	if e := s.st.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND tenant_id=$2 AND disabled_at IS NULL)`, auth.User(r), auth.Tenant(r)).Scan(&live); e != nil || !live {
		http.Error(w, "active tenant session required", 403)
		return false
	}
	if s.commissioning == nil || !s.commissioning.Config.Enabled {
		http.Error(w, "commissioning unavailable", 409)
		return false
	}
	service, e := enroll.NewCandidateService(r.Context(), s.commissioning.Config)
	if e != nil {
		http.Error(w, "commissioning readiness changed", 409)
		return false
	}
	service.Close()
	return true
}
func trustFingerprint(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func (s *server) commissioningReadiness(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.commissioningEligible(w, r) {
		return
	}
	writeJSON(w, 200, map[string]any{"server": s.commissioning.Origin, "tenant": auth.Tenant(r), "transport_ca": s.commissioning.TransportCA, "device_ca": s.commissioning.DeviceCA, "response_key": s.commissioning.ResponseKey, "claim_hours": 1, "result_minutes": 45, "certificate_days": 1, "state": "commissioning_available", "unattended": false, "transport_sha256": trustFingerprint([]byte(s.commissioning.TransportCA)), "device_sha256": trustFingerprint([]byte(s.commissioning.DeviceCA)), "response_key_sha256": trustFingerprint(s.commissioning.ResponseKey)})
}
func (s *server) mintCommissioning(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.commissioningEligible(w, r) {
		return
	}
	var in struct {
		Site   string `json:"site_id"`
		Serial string `json:"serial"`
		Kind   string `json:"kind"`
		TTL    int    `json:"ttl_hours"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 8193))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || in.Site == "" || in.Serial == "" || in.Kind != "edge" || in.TTL != 1 {
		http.Error(w, "explicit one-hour edge commissioning required", 400)
		return
	}
	var own bool
	if e := s.st.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM sites WHERE id=$1 AND tenant_id=$2)`, in.Site, auth.Tenant(r)).Scan(&own); e != nil || !own {
		http.Error(w, "site unavailable", 403)
		return
	}
	body, _ := json.Marshal(map[string]any{"site_id": in.Site, "serial": in.Serial, "kind": "edge", "ttl_hours": 1})
	copy := r.Clone(r.Context())
	copy.Body = io.NopCloser(bytes.NewReader(body))
	capture := &commissioningCapture{header: http.Header{}}
	s.mintEnrollmentToken(capture, copy)
	if capture.code != 201 {
		for k, v := range capture.header {
			w.Header()[k] = v
		}
		w.WriteHeader(capture.code)
		w.Write(capture.body.Bytes())
		return
	}
	var minted map[string]any
	if json.Unmarshal(capture.body.Bytes(), &minted) != nil {
		http.Error(w, "mint committed, preserve setup and contact operator", 500)
		return
	}
	delete(minted, "enroll_string")
	delete(minted, "fallback_urls")
	minted["server_url"] = s.commissioning.Origin
	minted["state"] = "claim_minted"
	minted["retained_input"] = map[string]any{"Server": s.commissioning.Origin, "Serial": in.Serial, "Code": minted["claim_code"], "TransportCA": s.commissioning.TransportCA, "DeviceCA": s.commissioning.DeviceCA, "ExpectedTenant": auth.Tenant(r), "ResponseKey": s.commissioning.ResponseKey}
	writeJSON(w, 201, minted)
}

type commissioningCapture struct {
	header http.Header
	body   bytes.Buffer
	code   int
}

func (c *commissioningCapture) Header() http.Header { return c.header }
func (c *commissioningCapture) WriteHeader(n int)   { c.code = n }
func (c *commissioningCapture) Write(b []byte) (int, error) {
	if c.code == 0 {
		c.code = 200
	}
	return c.body.Write(b)
}

func (s *server) commissioningReceipt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.commissioningEligible(w, r) {
		return
	}
	gateway, event := r.URL.Query().Get("gateway_id"), r.URL.Query().Get("event_id")
	if gateway == "" || event == "" || len(gateway) > 128 || len(event) > 128 {
		http.Error(w, "exact gateway/event required", 400)
		return
	}
	rows, e := s.st.Pool.Query(r.Context(), `SELECT t.event_id,t.gateway_id,t.tenant_id,t.device_id,t.point_id,t.value,t.unit,t.quality,t.observed_at,t.received_at FROM telemetry t JOIN gateways g ON g.id=t.gateway_id AND g.tenant_id=t.tenant_id WHERE t.tenant_id=$1 AND t.gateway_id=$2 AND t.event_id=$3 LIMIT 2`, auth.Tenant(r), gateway, event)
	if e != nil {
		http.Error(w, "receipt unavailable", 503)
		return
	}
	defer rows.Close()
	var result map[string]any
	count := 0
	for rows.Next() {
		var ev, gw, ten, dev, point, unit, quality string
		var val float64
		var observed, received time.Time
		if rows.Scan(&ev, &gw, &ten, &dev, &point, &val, &unit, &quality, &observed, &received) != nil {
			http.Error(w, "receipt unavailable", 503)
			return
		}
		count++
		result = map[string]any{"event_id": ev, "gateway_id": gw, "tenant_id": ten, "device_id": dev, "point_id": point, "value": val, "unit": unit, "quality": quality, "observed_at": observed, "received_at": received, "state": "exact_event_received", "event_source": "operator_or_controller_observed"}
	}
	if rows.Err() != nil {
		http.Error(w, "receipt unavailable", 503)
		return
	}
	if count != 1 {
		http.Error(w, "exact receipt unavailable", 404)
		return
	}
	writeJSON(w, 200, result)
}
