package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/commission"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/enroll"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/google/uuid"
)

// --- guided commissioning: site -> QR claim -> profile -> port test -> live ---

// createCommissionSession starts a wizard run: gateway row (pending), one-time
// claim code, and the session that tracks installer progress. The claim code
// and QR payload are returned once and never stored in plaintext.
func (s *server) createCommissionSession(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		SiteID string `json:"site_id"`
		Serial string `json:"serial"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.SiteID == "" || in.Serial == "" {
		http.Error(w, "site_id and serial required", 400)
		return
	}
	if len(in.Serial) > 64 || strings.ContainsAny(in.Serial, "<>\x00") {
		http.Error(w, "serial invalid", 400)
		return
	}
	tenant := auth.Tenant(r)
	code, err := enroll.NewCode()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	sessionID, gwID := uuid.NewString(), uuid.NewString()
	expires := time.Now().Add(72 * time.Hour)
	tx, err := s.st.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer tx.Rollback(r.Context())
	res, err := tx.Exec(r.Context(),
		`INSERT INTO gateways(id,tenant_id,site_id,serial,status)
		 SELECT $1,$2,$3,$4,'pending' WHERE EXISTS (SELECT 1 FROM sites WHERE id=$3 AND tenant_id=$2)`,
		gwID, tenant, in.SiteID, in.Serial)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if res.RowsAffected() == 0 {
		http.Error(w, "site not found", 404)
		return
	}
	if _, err := tx.Exec(r.Context(),
		`INSERT INTO enrollment_tokens(id,tenant_id,site_id,gateway_id,serial,code_hash,expires_at,created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		uuid.NewString(), tenant, in.SiteID, gwID, in.Serial, enroll.Hash(code), expires, auth.User(r)); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if _, err := tx.Exec(r.Context(),
		`INSERT INTO commissioning_sessions(id,tenant_id,site_id,gateway_id,serial,created_by)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		sessionID, tenant, in.SiteID, gwID, in.Serial, auth.User(r)); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit(r, "commission.start", sessionID, map[string]any{"serial": in.Serial, "site_id": in.SiteID})
	apiBase := envOr("API_PUBLIC_URL", "http://localhost:8000")
	writeJSON(w, 201, map[string]any{
		"session_id": sessionID, "gateway_id": gwID,
		"claim_code": code, "expires_at": expires,
		"qr_payload":    commission.QRPayload(apiBase, code, in.Serial),
		"enroll_string": enrollString(apiBase, code, in.Serial),
		"note":          "show the claim code to the installer once; only its hash is stored",
	})
}

// getCommissionSession returns the session with its wizard state derived from
// live evidence: claim status, device linkage, port-test result, telemetry.
func (s *server) getCommissionSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var siteID, gwID, serial, createdBy string
	var deviceID, profileID *string
	var portTest []byte
	var createdAt, updatedAt time.Time
	err := s.st.Pool.QueryRow(r.Context(),
		`SELECT site_id,gateway_id,serial,device_id,profile_id,port_test,created_by,created_at,updated_at
		 FROM commissioning_sessions WHERE id=$1 AND tenant_id=$2`, id, auth.Tenant(r)).
		Scan(&siteID, &gwID, &serial, &deviceID, &profileID, &portTest, &createdBy, &createdAt, &updatedAt)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	var gwStatus string
	s.st.Pool.QueryRow(r.Context(), `SELECT status FROM gateways WHERE id=$1`, gwID).Scan(&gwStatus)
	facts := commission.Facts{Claimed: gwStatus == "active", HasDevice: deviceID != nil}
	var testResult map[string]any
	if len(portTest) > 0 {
		var pt struct {
			Result *struct {
				OK bool `json:"ok"`
			} `json:"result"`
		}
		if json.Unmarshal(portTest, &pt) == nil && pt.Result != nil {
			facts.TestOK = &pt.Result.OK
			json.Unmarshal(portTest, &testResult)
		}
	}
	if deviceID != nil {
		s.st.Pool.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM telemetry WHERE tenant_id=$1 AND device_id=$2)`,
			auth.Tenant(r), *deviceID).Scan(&facts.Live)
	}
	writeJSON(w, 200, map[string]any{
		"session_id": id, "site_id": siteID, "gateway_id": gwID, "serial": serial,
		"gateway_status": gwStatus, "device_id": deviceID, "profile_id": profileID,
		"port_test": testResult, "state": commission.DeriveState(facts),
		"created_by": createdBy, "created_at": createdAt, "updated_at": updatedAt,
	})
}

// assignCommissionProfile attaches a device profile to the session's gateway:
// creates the device with its points, ready for edge config sync.
func (s *server) assignCommissionProfile(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	id := r.PathValue("id")
	var in struct {
		ProfileID  string `json:"profile_id"`
		DeviceName string `json:"device_name"`
		// Connection: port/baud/address (serial) or host/endpoint (network).
		Connection map[string]any `json:"connection"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.ProfileID == "" || in.DeviceName == "" {
		http.Error(w, "profile_id and device_name required", 400)
		return
	}
	tenant := auth.Tenant(r)
	var gwID string
	var existing *string
	if err := s.st.Pool.QueryRow(r.Context(),
		`SELECT gateway_id, device_id FROM commissioning_sessions WHERE id=$1 AND tenant_id=$2`, id, tenant).
		Scan(&gwID, &existing); err != nil {
		http.Error(w, "session not found", 404)
		return
	}
	if existing != nil {
		http.Error(w, "session already has a device", 409)
		return
	}
	var driverProfile string
	var points []byte
	if err := s.st.Pool.QueryRow(r.Context(),
		`SELECT driver_profile, points FROM device_profiles WHERE id=$1 AND tenant_id=$2`, in.ProfileID, tenant).
		Scan(&driverProfile, &points); err != nil {
		http.Error(w, "profile not found", 404)
		return
	}
	if in.Connection != nil {
		if err := validConnection(driverProfile, in.Connection); err != nil {
			http.Error(w, "connection: "+err.Error(), 400)
			return
		}
	}
	devID := uuid.NewString()
	cfg, _ := json.Marshal(map[string]any{"device_profile_id": in.ProfileID, "connection": in.Connection})
	tx, err := s.st.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(),
		`INSERT INTO devices(id,tenant_id,gateway_id,profile,name,config) VALUES($1,$2,$3,$4,$5,$6)`,
		devID, tenant, gwID, driverProfile, in.DeviceName, cfg); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// Points come from the profile template; each becomes a validated stream.
	var pts []struct {
		ID   string  `json:"id"`
		Unit string  `json:"unit"`
		Min  float64 `json:"min"`
		Max  float64 `json:"max"`
	}
	if err := json.Unmarshal(points, &pts); err != nil {
		http.Error(w, "profile points corrupt", 500)
		return
	}
	for _, p := range pts {
		if _, err := tx.Exec(r.Context(),
			`INSERT INTO points(id,device_id,unit,min_value,max_value) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`,
			p.ID, devID, p.Unit, p.Min, p.Max); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	tag, err := tx.Exec(r.Context(),
		`UPDATE commissioning_sessions SET device_id=$1, profile_id=$2, state='profiled', updated_at=now()
		 WHERE id=$3 AND device_id IS NULL`, devID, in.ProfileID, id)
	if err != nil || tag.RowsAffected() != 1 {
		http.Error(w, "session changed concurrently", 409)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit(r, "commission.profile", id, map[string]any{"device_id": devID, "profile_id": in.ProfileID})
	writeJSON(w, 201, map[string]any{"device_id": devID, "points": len(pts)})
}

// requestPortTest publishes a read-only diagnostic probe to the session's
// gateway. The edge agent answers on .../diag/result; ingest records it.
func (s *server) requestPortTest(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	id := r.PathValue("id")
	var p commission.Probe
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if err := commission.ValidateProbe(p); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	tenant := auth.Tenant(r)
	var gwID, gwStatus string
	if err := s.st.Pool.QueryRow(r.Context(),
		`SELECT gateway_id, (SELECT status FROM gateways WHERE id=gateway_id)
		 FROM commissioning_sessions WHERE id=$1 AND tenant_id=$2`, id, tenant).
		Scan(&gwID, &gwStatus); err != nil {
		http.Error(w, "session not found", 404)
		return
	}
	if gwStatus != "active" {
		http.Error(w, "gateway has not claimed yet; port test needs a live gateway", 409)
		return
	}
	p.SessionID = id
	cfgJSON, _ := json.Marshal(p)
	if _, err := s.st.Pool.Exec(r.Context(),
		`UPDATE commissioning_sessions
		 SET port_test = jsonb_build_object('requested_at', now(), 'config', $1::jsonb), updated_at=now()
		 WHERE id=$2`, cfgJSON, id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	topic := fmt.Sprintf("t/%s/g/%s/diag", tenant, gwID)
	if err := s.publishMQTT(topic, cfgJSON); err != nil {
		http.Error(w, "broker unreachable: "+err.Error(), 502)
		return
	}
	s.audit(r, "commission.port_test", id, map[string]any{"port": p.Port, "address": p.Address, "register": p.Register})
	writeJSON(w, 202, map[string]any{"status": "probe sent", "session_id": id})
}

// commissionPreview is the wizard's final step: latest reading per point for
// the session's device, proving the sensor is live end to end.
func (s *server) commissionPreview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var deviceID *string
	if err := s.st.Pool.QueryRow(r.Context(),
		`SELECT device_id FROM commissioning_sessions WHERE id=$1 AND tenant_id=$2`, id, auth.Tenant(r)).
		Scan(&deviceID); err != nil {
		http.Error(w, "session not found", 404)
		return
	}
	if deviceID == nil {
		http.Error(w, "no device assigned yet", 409)
		return
	}
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT DISTINCT ON (point_id) point_id, value, unit, quality, observed_at
		 FROM telemetry WHERE tenant_id=$1 AND device_id=$2
		 ORDER BY point_id, observed_at DESC`, auth.Tenant(r), *deviceID)
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
	writeJSON(w, 200, map[string]any{"session_id": id, "device_id": *deviceID, "live": len(out) > 0, "points": out})
}

// --- MQTT publisher (diag probes only; telemetry never flows through here) ---

var (
	mqttOnce   sync.Once
	mqttClient mqtt.Client
	mqttErr    error
)

func (s *server) publishMQTT(topic string, payload []byte) error {
	return s.publishMQTTRetained(topic, payload, false)
}

// publishMQTTRetained publishes with the retained flag set - used for fleet
// manifests, which a gateway must receive even while offline at fanout time.
func (s *server) publishMQTTRetained(topic string, payload []byte, retained bool) error {
	mqttOnce.Do(func() {
		host, port := os.Getenv("MQTT_HOST"), os.Getenv("MQTT_PORT")
		if host == "" {
			mqttErr = fmt.Errorf("MQTT_HOST not configured")
			return
		}
		if port == "" {
			port = "1883"
		}
		opts := mqtt.NewClientOptions().
			AddBroker("tcp://" + host + ":" + port).
			SetClientID("api-commission").SetAutoReconnect(true).SetConnectRetry(false)
		mqttClient = mqtt.NewClient(opts)
		if tok := mqttClient.Connect(); tok.Wait() && tok.Error() != nil {
			mqttErr = tok.Error()
		}
	})
	if mqttErr != nil {
		return mqttErr
	}
	tok := mqttClient.Publish(topic, 1, retained, payload)
	tok.Wait()
	return tok.Error()
}

// listSites feeds the commissioning wizard's site picker.
func (s *server) listSites(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT id, name FROM sites WHERE tenant_id=$1 ORDER BY name`, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name string
		rows.Scan(&id, &name)
		out = append(out, map[string]any{"id": id, "name": name})
	}
	writeJSON(w, 200, out)
}
