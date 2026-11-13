package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/fleet"
)

// edgeSelfSetup is the enrolled gateway's own read-only bootstrap. It never
// creates identity, changes network exposure, or enables discovery or commands.
func (s *server) edgeSelfSetup(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "gateway") || auth.ViaKey(r) {
		if auth.ViaKey(r) {
			http.Error(w, "gateway identity required", 403)
		}
		return
	}
	tenant, gw := auth.Tenant(r), auth.User(r)
	var serial, status string
	if err := s.st.Pool.QueryRow(r.Context(), `SELECT serial,status FROM gateways WHERE id=$1 AND tenant_id=$2`, gw, tenant).Scan(&serial, &status); err != nil {
		http.Error(w, "gateway not found", 404)
		return
	}
	if status != "active" {
		http.Error(w, "gateway identity is not active", 403)
		return
	}
	host := strings.TrimSpace(os.Getenv("EDGE_MQTT_HOST"))
	port, err := strconv.Atoi(os.Getenv("EDGE_MQTT_PORT"))
	ip := net.ParseIP(host)
	if host == "" || !hostRe.MatchString(host) || strings.EqualFold(host, "localhost") || (ip != nil && (ip.IsLoopback() || ip.IsUnspecified())) || err != nil || port < 1 || port > 65535 || os.Getenv("EDGE_MQTT_TLS") != "true" {
		http.Error(w, "secure gateway connection is not configured on this server; an administrator must set the reachable TLS broker address and port", 409)
		return
	}
	key, err := fleet.SigningKeyFromEnv()
	if err != nil {
		http.Error(w, "source update signing is misconfigured on this server", 409)
		return
	}
	public := ""
	if key != nil {
		public = base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
	}
	devs, err := s.gatewayEdgeDevices(r, tenant, gw)
	if err != nil {
		dbErr(w, err)
		return
	}
	y := renderEdgeYAML(tenant, gw, serial, devs)
	safe := "autodetect:\n  enabled: false\nallow_automatic_commands: false\nremote_restart: false\nmanaged_devices: false\noutputs: []\nrules: []\n"
	if public != "" {
		safe += "fleet_public_key: " + q(public) + "\n"
	}
	y = strings.Replace(y, "devices:\n", safe+"devices:\n", 1)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"gateway_id": gw, "tenant_id": tenant, "serial": serial, "config_yaml": y, "signed_source_updates_available": public != ""})
}
