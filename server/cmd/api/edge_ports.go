package main

import (
	"encoding/json"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"net/http"
	"time"
)

func (s *server) reportSerialPorts(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.migrationGateway(w, r); !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var in struct {
		Ports []string `json:"ports"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Ports) > 64 {
		http.Error(w, "invalid serial inventory", 400)
		return
	}
	seen := map[string]bool{}
	ports := []string{}
	for _, p := range in.Ports {
		if !portRe.MatchString(p) {
			http.Error(w, "invalid serial port name", 400)
			return
		}
		if !seen[p] {
			seen[p] = true
			ports = append(ports, p)
		}
	}
	raw, _ := json.Marshal(ports)
	_, e := s.st.Pool.Exec(r.Context(), `INSERT INTO edge_serial_port_inventory(gateway_id,tenant_id,ports) VALUES($1,$2,$3) ON CONFLICT(gateway_id) DO UPDATE SET ports=EXCLUDED.ports,reported_at=now() WHERE edge_serial_port_inventory.tenant_id=EXCLUDED.tenant_id`, auth.User(r), auth.Tenant(r), raw)
	if e != nil {
		dbErr(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"status": "reported", "note": "OS inventory only; no ports were opened or probed"})
}
func (s *server) getSerialPorts(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var raw []byte
	var at time.Time
	e := s.st.Pool.QueryRow(r.Context(), `SELECT p.ports,p.reported_at FROM edge_serial_port_inventory p JOIN gateways g ON g.id=p.gateway_id AND g.tenant_id=p.tenant_id WHERE p.gateway_id=$1 AND p.tenant_id=$2 AND g.status='active'`, r.PathValue("id"), auth.Tenant(r)).Scan(&raw, &at)
	if e != nil {
		http.Error(w, "No current port inventory. On the intended PC use Send USB ports to dashboard in the edge manager.", 404)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	fresh := !at.Before(time.Now().Add(-5*time.Minute)) && !at.After(time.Now().Add(5*time.Second))
	ports := json.RawMessage(raw)
	if !fresh {
		ports = json.RawMessage(`[]`)
	}
	writeJSON(w, 200, map[string]any{"ports": ports, "reported_at": at, "fresh": fresh, "note": "OS inventory, not a verified meter. No automatic selection or polling."})
}
