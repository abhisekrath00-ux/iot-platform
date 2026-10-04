package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// Dashboard discovery scans. The API validates and publishes a read-only scan
// request to the gateway; the gateway answers on scan/result (stored by the
// ingest service). Nothing is added automatically: an operator picks a result,
// a profile and confirms (addScannedDevice).

type scanParams struct {
	// modbus-rtu
	Port     string `json:"port,omitempty"`
	Baud     int    `json:"baud,omitempty"`
	DataBits int    `json:"data_bits,omitempty"`
	StopBits int    `json:"stop_bits,omitempty"`
	Parity   string `json:"parity,omitempty"`
	From     int    `json:"from,omitempty"`
	To       int    `json:"to,omitempty"`
	// lan
	CIDR string `json:"cidr,omitempty"`
	// bacnet
	Broadcast string `json:"broadcast,omitempty"`
}

var stdBaud = map[int]bool{1200: true, 2400: true, 4800: true, 9600: true, 19200: true, 38400: true, 57600: true, 115200: true}

func validateScan(kind string, p *scanParams) error {
	switch kind {
	case "modbus-rtu":
		if !portRe.MatchString(p.Port) {
			return fmt.Errorf("port must be /dev/... or COMn")
		}
		if !stdBaud[p.Baud] {
			return fmt.Errorf("baud must be a standard rate (1200-115200)")
		}
		if p.DataBits == 0 {
			p.DataBits = 8
		}
		if p.StopBits == 0 {
			p.StopBits = 1
		}
		if p.DataBits < 5 || p.DataBits > 8 || (p.StopBits != 1 && p.StopBits != 2) {
			return fmt.Errorf("data_bits 5-8, stop_bits 1 or 2")
		}
		switch p.Parity {
		case "none", "odd", "even":
		case "":
			p.Parity = "none"
		default:
			return fmt.Errorf("parity must be none|odd|even")
		}
		if p.From == 0 {
			p.From = 1
		}
		if p.To == 0 {
			p.To = 32
		}
		if p.From < 1 || p.To > 247 || p.From > p.To {
			return fmt.Errorf("address range must be within 1-247")
		}
	case "lan":
		pf, err := netip.ParsePrefix(p.CIDR)
		if err != nil || !pf.Addr().Is4() || !pf.Addr().IsPrivate() || pf.Bits() < 22 {
			return fmt.Errorf("cidr must be a private IPv4 range, /22 or smaller (e.g. 192.168.1.0/24)")
		}
	case "bacnet":
		ip := net.ParseIP(p.Broadcast)
		if ip == nil || ip.To4() == nil {
			return fmt.Errorf("broadcast must be an IPv4 address (e.g. 192.168.1.255)")
		}
	default:
		return fmt.Errorf("kind must be modbus-rtu, lan or bacnet")
	}
	return nil
}

// POST /v1/gateways/{id}/scans
func (s *server) requestScan(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	gw, tenant := r.PathValue("id"), auth.Tenant(r)
	var in struct {
		Kind   string     `json:"kind"`
		Params scanParams `json:"params"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if err := validateScan(in.Kind, &in.Params); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var status string
	if err := s.st.Pool.QueryRow(r.Context(), `SELECT status FROM gateways WHERE id=$1 AND tenant_id=$2`, gw, tenant).Scan(&status); err != nil {
		http.Error(w, "gateway not found", 404)
		return
	}
	if status != "active" {
		http.Error(w, "gateway has not claimed yet; scanning needs a live gateway", 409)
		return
	}
	var running int
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM gateway_scans WHERE gateway_id=$1 AND tenant_id=$2 AND status='requested' AND created_at > now() - interval '10 minutes'`, gw, tenant).Scan(&running)
	if running > 0 {
		http.Error(w, "a scan is already running on this gateway; wait for it to finish", 409)
		return
	}
	id := uuid.NewString()
	pj, _ := json.Marshal(in.Params)
	if _, err := s.st.Pool.Exec(r.Context(),
		`INSERT INTO gateway_scans(id,tenant_id,gateway_id,kind,params,requested_by) VALUES($1,$2,$3,$4,$5,$6)`,
		id, tenant, gw, in.Kind, pj, auth.User(r)); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	req := map[string]any{"scan_id": id, "kind": in.Kind}
	switch in.Kind {
	case "modbus-rtu":
		req["port"], req["baud"], req["data_bits"], req["stop_bits"], req["parity"] = in.Params.Port, in.Params.Baud, in.Params.DataBits, in.Params.StopBits, in.Params.Parity
		req["from"], req["to"] = in.Params.From, in.Params.To
		req["candidates"] = s.scanCandidates(r, tenant)
	case "lan":
		req["cidr"] = in.Params.CIDR
	case "bacnet":
		req["broadcast"] = in.Params.Broadcast
	}
	payload, _ := json.Marshal(req)
	if err := s.publishMQTT(fmt.Sprintf("t/%s/g/%s/scan", tenant, gw), payload); err != nil {
		s.st.Pool.Exec(r.Context(), `UPDATE gateway_scans SET status='failed', result=$1, updated_at=now() WHERE id=$2`, `{"ok":false,"error":"broker unreachable"}`, id)
		http.Error(w, "broker unreachable: "+err.Error(), 502)
		return
	}
	s.audit(r, "gateway.scan.request", gw, map[string]any{"kind": in.Kind, "scan_id": id})
	writeJSON(w, 202, map[string]any{"scan_id": id, "status": "requested"})
}

// scanCandidates lists the tenant's Modbus profiles (first points only) so the
// edge can score which one an answering slave resembles.
func (s *server) scanCandidates(r *http.Request, tenant string) []map[string]any {
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT id, name, points FROM device_profiles WHERE tenant_id=$1 AND driver_profile IN ('modbus-generic','modbus-energy-meter') ORDER BY created_at DESC LIMIT 20`, tenant)
	out := []map[string]any{}
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		var pj []byte
		if rows.Scan(&id, &name, &pj) != nil {
			continue
		}
		var pts []map[string]any
		if json.Unmarshal(pj, &pts) != nil || len(pts) == 0 {
			continue
		}
		if len(pts) > 3 {
			pts = pts[:3]
		}
		keep := []map[string]any{}
		for _, p := range pts {
			k := map[string]any{}
			for _, f := range []string{"id", "register", "func", "type", "word_order", "scale", "min", "max"} {
				if v, ok := p[f]; ok {
					k[f] = v
				}
			}
			if _, ok := k["func"]; !ok {
				k["func"] = 4.0
			}
			keep = append(keep, k)
		}
		out = append(out, map[string]any{"profile_id": id, "name": name, "points": keep})
	}
	return out
}

func scanRowJSON(id, gw, kind, status string, params, result []byte, created, updated time.Time) map[string]any {
	m := map[string]any{"id": id, "gateway_id": gw, "kind": kind, "status": status, "created_at": created, "updated_at": updated}
	m["params"] = json.RawMessage(params)
	if result != nil {
		m["result"] = json.RawMessage(result)
	}
	// A request the gateway never answered is shown as timed out, not forever pending.
	if status == "requested" && time.Since(created) > 10*time.Minute {
		m["status"] = "timed_out"
	}
	return m
}

// GET /v1/gateways/{id}/scans: latest scans, newest first.
func (s *server) listScans(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT id, gateway_id, kind, status, params, result, created_at, updated_at FROM gateway_scans
		 WHERE tenant_id=$1 AND gateway_id=$2 ORDER BY created_at DESC LIMIT 10`, auth.Tenant(r), r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, gw, kind, status string
		var p, res []byte
		var c, u time.Time
		if rows.Scan(&id, &gw, &kind, &status, &p, &res, &c, &u) == nil {
			out = append(out, scanRowJSON(id, gw, kind, status, p, res, c, u))
		}
	}
	writeJSON(w, 200, out)
}

// POST /v1/gateways/{id}/scans/{sid}/add: the one-click add. The operator picks a
// found device (by what they saw in the scan result) and a profile; the server
// re-validates the connection and creates the device with the profile's points.
func (s *server) addScannedDevice(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	gw, sid, tenant := r.PathValue("id"), r.PathValue("sid"), auth.Tenant(r)
	var in struct {
		ProfileID  string         `json:"profile_id"`
		DeviceName string         `json:"device_name"`
		Connection map[string]any `json:"connection"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in); err != nil || in.ProfileID == "" || in.DeviceName == "" || len(in.DeviceName) > 128 {
		http.Error(w, "profile_id and device_name required", 400)
		return
	}
	var kind string
	var result []byte
	if err := s.st.Pool.QueryRow(r.Context(),
		`SELECT kind, result FROM gateway_scans WHERE id=$1 AND tenant_id=$2 AND gateway_id=$3 AND status='done'`, sid, tenant, gw).Scan(&kind, &result); err != nil {
		http.Error(w, "finished scan not found", 404)
		return
	}
	var driverProfile string
	var pj []byte
	if err := s.st.Pool.QueryRow(r.Context(), `SELECT driver_profile, points FROM device_profiles WHERE id=$1 AND tenant_id=$2`, in.ProfileID, tenant).Scan(&driverProfile, &pj); err != nil {
		http.Error(w, "profile not found", 404)
		return
	}
	if err := validConnection(driverProfile, in.Connection); err != nil {
		http.Error(w, "connection: "+err.Error(), 400)
		return
	}
	if !connectionFromScan(kind, result, in.Connection) {
		http.Error(w, "that device was not in this scan's results", 409)
		return
	}
	// The same bus address or host:port twice on one gateway is almost always a mistake.
	var dup int
	s.st.Pool.QueryRow(r.Context(),
		`SELECT count(*) FROM devices WHERE tenant_id=$1 AND gateway_id=$2 AND config->'connection'->>'port' IS NOT DISTINCT FROM $3
		   AND config->'connection'->>'host' IS NOT DISTINCT FROM $4 AND config->'connection'->>'address' IS NOT DISTINCT FROM $5`,
		tenant, gw, strOrNil(in.Connection["port"]), strOrNil(in.Connection["host"]), numStrOrNil(in.Connection["address"])).Scan(&dup)
	if dup > 0 {
		http.Error(w, "a device with this connection already exists on the gateway", 409)
		return
	}
	var pts []struct {
		ID   string  `json:"id"`
		Unit string  `json:"unit"`
		Min  float64 `json:"min"`
		Max  float64 `json:"max"`
	}
	if json.Unmarshal(pj, &pts) != nil {
		http.Error(w, "profile points corrupt", 500)
		return
	}
	if !s.quotaOK(w, r, "devices", 1) {
		return
	}
	devID := uuid.NewString()
	cfg, _ := json.Marshal(map[string]any{"device_profile_id": in.ProfileID, "connection": in.Connection, "added_from_scan": sid})
	tx, err := s.st.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(), `INSERT INTO devices(id,tenant_id,gateway_id,profile,name,config) VALUES($1,$2,$3,$4,$5,$6)`,
		devID, tenant, gw, driverProfile, in.DeviceName, cfg); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	for _, p := range pts {
		if _, err := tx.Exec(r.Context(), `INSERT INTO points(id,device_id,unit,min_value,max_value) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, p.ID, devID, p.Unit, p.Min, p.Max); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit(r, "gateway.scan.add_device", devID, map[string]any{"scan_id": sid, "profile_id": in.ProfileID})
	writeJSON(w, 201, map[string]any{"id": devID, "note": "download the gateway's edge config again (or push it as a fleet config) to start polling"})
}

func strOrNil(v any) *string {
	if s, ok := v.(string); ok && s != "" {
		return &s
	}
	return nil
}

func numStrOrNil(v any) *string {
	if f, ok := v.(float64); ok {
		s := fmt.Sprint(int(f))
		return &s
	}
	return nil
}

// connectionFromScan checks that the connection the operator submits points at
// something the scan actually found, so the add cannot be used to attach an
// arbitrary host or port to the gateway under the cover of a scan.
func connectionFromScan(kind string, result []byte, conn map[string]any) bool {
	var res struct {
		Slaves []struct {
			Address int `json:"address"`
		} `json:"slaves"`
		Hosts []struct {
			Addr string `json:"Addr"`
			Port int    `json:"Port"`
		} `json:"hosts"`
		BACnet []struct {
			Addr string `json:"Addr"`
		} `json:"bacnet"`
	}
	if json.Unmarshal(result, &res) != nil {
		return false
	}
	switch kind {
	case "modbus-rtu":
		a, ok := conn["address"].(float64)
		if !ok {
			return false
		}
		for _, sl := range res.Slaves {
			if float64(sl.Address) == a {
				return true
			}
		}
	case "lan":
		h, _ := conn["host"].(string)
		p, hasPort := conn["net_port"].(float64)
		for _, x := range res.Hosts {
			if x.Addr == h && (!hasPort || float64(x.Port) == p) {
				return true
			}
		}
	case "bacnet":
		h, _ := conn["host"].(string)
		for _, x := range res.BACnet {
			if x.Addr == h {
				return true
			}
		}
	}
	return false
}
