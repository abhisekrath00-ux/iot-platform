package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// Protocol families the edge agent can poll. Serial ones need a port; network
// ones need a host/endpoint. serial-json is the STM32/Arduino/ESP32 path.
var driverKinds = map[string]string{
	"modbus-generic": "serial", "modbus-energy-meter": "serial", "door-contact": "serial",
	"serial-json": "serial", "modbus-tcp": "net", "opcua": "net",
	// Network drivers added later. Tested against simulators only; see docs/connectors.md.
	"snmp": "net", "bacnet": "net", "iec104": "net", "dnp3": "net", "coap": "net", "iec61850": "net", "lwm2m": "net", "can": "can",
}

var (
	hostRe     = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,253}$`)
	portRe     = regexp.MustCompile(`^(/dev/[A-Za-z0-9._/-]{1,64}|COM[0-9]{1,3})$`)
	epRe       = regexp.MustCompile(`^opc\.tcp://[A-Za-z0-9._:\-\[\]]{1,253}(/[A-Za-z0-9._~/\-]{0,128})?$`)
	nodeRe     = regexp.MustCompile(`^(ns=[0-9]{1,5};)?[isgb]=[A-Za-z0-9._:/\- \[\]]{1,128}$`)
	oidRe      = regexp.MustCompile(`^\.[0-9]{1,10}(\.[0-9]{1,10}){2,30}$`)
	envRe      = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
	pkeyRe     = regexp.MustCompile(`^[A-Za-z0-9_.:/#\-\[\]$ ]{1,200}$`)
	canIfaceRe = regexp.MustCompile(`^[a-z]{1,8}[0-9]{1,3}$`)
	lwm2mKeyRe = regexp.MustCompile(`^/[0-9]{1,5}/[0-9]{1,5}/[0-9]{1,5}$`)
	canKeyRe   = regexp.MustCompile(`^0x[0-9A-Fa-f]{1,8}:[0-9]{1,2}:[0-9]{1,2}:(le|be):[us]$`)
	keyRe      = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,64}$`)
)

// validateProfilePoints checks points against the driver family's schema.
func validateProfilePointsFor(driver string, pts []map[string]any) error {
	if driver == "opcua" || driver == "serial-json" || driver == "modbus-tcp" || netExtra[driver] {
		// The edge agent refuses points without a validation range; fail here,
		// not on the gateway after deployment.
		for _, p := range pts {
			mn, ok1 := p["min"].(float64)
			mx, ok2 := p["max"].(float64)
			if !ok1 || !ok2 || mx <= mn {
				return fmt.Errorf("point %v: min and max required (max > min)", p["id"])
			}
		}
	}
	if netExtra[driver] {
		return validateExtraPoints(driver, pts)
	}
	switch driver {
	case "opcua":
		if len(pts) == 0 || len(pts) > 64 {
			return fmt.Errorf("1-64 points required")
		}
		for _, p := range pts {
			id, _ := p["id"].(string)
			if !profilePointOK.MatchString(id) {
				return fmt.Errorf("point id %q invalid", id)
			}
			n, _ := p["node_id"].(string)
			if !nodeRe.MatchString(n) {
				return fmt.Errorf("point %s: node_id must look like ns=2;s=Boiler.Temp", id)
			}
		}
		return nil
	case "serial-json":
		if len(pts) == 0 || len(pts) > 64 {
			return fmt.Errorf("1-64 points required")
		}
		for _, p := range pts {
			id, _ := p["id"].(string)
			if !profilePointOK.MatchString(id) {
				return fmt.Errorf("point id %q invalid", id)
			}
			if k, ok := p["key"].(string); ok && k != "" && !keyRe.MatchString(k) {
				return fmt.Errorf("point %s: key invalid", id)
			}
		}
		return nil
	}
	return validProfilePoints(pts)
}

var netExtra = map[string]bool{"snmp": true, "bacnet": true, "iec104": true, "dnp3": true, "coap": true, "iec61850": true, "lwm2m": true, "can": true}

// validateExtraPoints checks the point addressing for the later network
// drivers. The edge agent re-validates everything; this only fails early.
func validateExtraPoints(driver string, pts []map[string]any) error {
	if len(pts) == 0 || len(pts) > 64 {
		return fmt.Errorf("1-64 points required")
	}
	for _, p := range pts {
		id, _ := p["id"].(string)
		if !profilePointOK.MatchString(id) {
			return fmt.Errorf("point id %q invalid", id)
		}
		key, _ := p["key"].(string)
		switch driver {
		case "snmp":
			if o, _ := p["oid"].(string); !oidRe.MatchString(o) {
				return fmt.Errorf("point %s: oid must be numeric like .1.3.6.1.2.1.1.3.0", id)
			}
		case "iec104":
			if v, ok := p["ioa"].(float64); !ok || v < 1 || v > 16777215 || v != float64(int(v)) {
				return fmt.Errorf("point %s: ioa must be 1-16777215", id)
			}
		case "dnp3":
			switch key {
			case "ai", "bi", "ctr", "bo":
			default:
				return fmt.Errorf("point %s: key must be ai|bi|ctr|bo", id)
			}
			if v, ok := p["register"].(float64); !ok || v < 0 || v > 65535 {
				return fmt.Errorf("point %s: register (point index) must be 0-65535", id)
			}
		default: // bacnet, coap, iec61850 address by key
			if !pkeyRe.MatchString(key) {
				return fmt.Errorf("point %s: key required (bacnet ai:1, coap path#field, iec61850 domain/item)", id)
			}
			if driver == "lwm2m" && !lwm2mKeyRe.MatchString(key) {
				return fmt.Errorf("point %s: lwm2m key must be /object/instance/resource such as /3303/0/5700", id)
			}
			if driver == "can" && !canKeyOK(key) {
				return fmt.Errorf("point %s: can key must be id:start:length:le|be:u|s such as 0x123:0:16:le:u", id)
			}
			if driver == "iec61850" && !strings.Contains(key, "/") {
				return fmt.Errorf("point %s: iec61850 key must be domain/item", id)
			}
		}
	}
	return nil
}

// validConnection whitelists per-device connection settings.
func validConnection(driver string, c map[string]any) error {
	kind := driverKinds[driver]
	str := func(k string) string { v, _ := c[k].(string); return v }
	num := func(k string) (float64, bool) { v, ok := c[k].(float64); return v, ok }
	for k := range c {
		switch k {
		case "port", "baud", "data_bits", "stop_bits", "parity", "address", "host", "net_port", "endpoint", "interval_seconds":
		case "snmp_version", "community_env", "snmp_auth", "snmp_auth_pass_env", "snmp_priv", "snmp_priv_pass_env":
			if driver != "snmp" {
				return fmt.Errorf("%s is only valid for snmp", k)
			}
			v, _ := c[k].(string)
			if !snmpFieldOK(k, v) {
				return fmt.Errorf("%s invalid", k)
			}
		default:
			return fmt.Errorf("unknown connection field %q", k)
		}
	}
	if iv, ok := num("interval_seconds"); ok && (iv < 1 || iv > 86400) {
		return fmt.Errorf("interval_seconds must be 1-86400")
	}
	if a, ok := num("address"); ok && (a < 0 || a > 255) {
		return fmt.Errorf("address must be 0-255")
	}
	switch kind {
	case "serial":
		if !portRe.MatchString(str("port")) {
			return fmt.Errorf("port must be /dev/... or COMn")
		}
		if b, ok := num("baud"); ok && (b < 300 || b > 4000000) {
			return fmt.Errorf("baud out of range")
		}
		switch str("parity") {
		case "", "none", "odd", "even":
		default:
			return fmt.Errorf("parity must be none|odd|even")
		}
	case "can":
		if !canIfaceRe.MatchString(str("port")) {
			return fmt.Errorf("port must be a CAN interface name such as can0 or vcan0")
		}
	case "net":
		if driver == "opcua" {
			if ep := str("endpoint"); ep != "" {
				if !epRe.MatchString(ep) {
					return fmt.Errorf("endpoint must be opc.tcp://host:port")
				}
				return nil
			}
		}
		if !hostRe.MatchString(str("host")) {
			return fmt.Errorf("host required")
		}
		if p, ok := num("net_port"); ok && (p < 1 || p > 65535) {
			return fmt.Errorf("net_port out of range")
		}
	}
	return nil
}

func snmpFieldOK(k, v string) bool {
	switch k {
	case "snmp_version":
		return v == "2c" || v == "3"
	case "snmp_auth":
		return v == "sha" || v == "sha256" || v == "sha512"
	case "snmp_priv":
		return v == "aes" || v == "aes256"
	}
	return envRe.MatchString(v) // secrets are env var NAMES on the box, never values
}

func q(v any) string { b, _ := json.Marshal(v); return string(b) } // JSON scalars are valid YAML

// renderEdgeYAML turns devices + profiles into the agent's config file.
func renderEdgeYAML(tenant, gateway, serial string, devs []edgeDev) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Generated by HexThings %s. Identity + mTLS files are picked up from the\n# agent data dir after enrollment; re-download after adding devices.\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "gateway_id: %s\ntenant_id: %s\nserial: %s\n", q(gateway), q(tenant), q(serial))
	host := os.Getenv("EDGE_MQTT_HOST")
	if host == "" {
		host = "localhost"
	}
	port := os.Getenv("EDGE_MQTT_PORT")
	if _, err := strconv.Atoi(port); err != nil {
		port = "8883"
	}
	tls := os.Getenv("EDGE_MQTT_TLS") != "false"
	fmt.Fprintf(&b, "mqtt:\n  host: %s\n  port: %s\n  tls: %v\nallowed_commands: []\ndevices:\n", q(host), port, tls)
	if len(devs) == 0 {
		b.WriteString("  []\n")
	}
	for _, d := range devs {
		fmt.Fprintf(&b, "  - id: %s\n    profile: %s\n", q(d.ID), q(d.Profile))
		iv := 10
		if v, ok := d.Conn["interval_seconds"].(float64); ok {
			iv = int(v)
		}
		fmt.Fprintf(&b, "    interval: %ds\n", iv)
		for _, k := range []string{"port", "parity", "host", "endpoint", "snmp_version", "community_env", "snmp_auth", "snmp_auth_pass_env", "snmp_priv", "snmp_priv_pass_env"} {
			if v, ok := d.Conn[k].(string); ok && v != "" {
				fmt.Fprintf(&b, "    %s: %s\n", k, q(v))
			}
		}
		for _, k := range []string{"baud", "data_bits", "stop_bits", "address", "net_port"} {
			if v, ok := d.Conn[k].(float64); ok {
				fmt.Fprintf(&b, "    %s: %d\n", k, int(v))
			}
		}
		if driverKinds[d.Profile] == "serial" {
			if _, ok := d.Conn["data_bits"]; !ok {
				b.WriteString("    data_bits: 8\n")
			}
			if _, ok := d.Conn["baud"]; !ok {
				b.WriteString("    baud: 9600\n")
			}
		}
		b.WriteString("    points:\n")
		for _, p := range d.Points {
			b.WriteString("      - {")
			first := true
			for _, k := range []string{"id", "register", "func", "type", "word_order", "key", "node_id", "oid", "ioa", "scale", "unit", "min", "max"} {
				if v, ok := p[k]; ok {
					if !first {
						b.WriteString(", ")
					}
					first = false
					fmt.Fprintf(&b, "%s: %s", k, q(v))
				}
			}
			b.WriteString("}\n")
		}
	}
	return b.String()
}

type edgeDev struct {
	ID, Profile string
	Conn        map[string]any
	Points      []map[string]any
}

// GET /v1/gateways/{id}/edge-config: ready-to-run agent YAML for a gateway.
func (s *server) gatewayEdgeConfig(w http.ResponseWriter, r *http.Request) {
	tenant := auth.Tenant(r)
	gw := r.PathValue("id")
	var serial string
	if err := s.st.Pool.QueryRow(r.Context(),
		`SELECT serial FROM gateways WHERE id=$1 AND tenant_id=$2`, gw, tenant).Scan(&serial); err != nil {
		http.Error(w, "gateway not found", 404)
		return
	}
	devs, err := s.gatewayEdgeDevices(r, tenant, gw)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit(r, "gateway.edge_config.download", gw, nil)
	w.Header().Set("Content-Type", "application/x-yaml")
	w.Header().Set("Content-Disposition", `attachment; filename="edge-agent.yaml"`)
	yml := renderEdgeYAML(tenant, gw, serial, devs)
	if outs, rules, ok := s.loadEdgeRules(r, gw); ok {
		yml += renderRulesYAML(outs, rules)
	}
	fmt.Fprint(w, yml)
}

// canKeyOK checks the key shape and that the signal fits in an 8-byte frame.
func canKeyOK(key string) bool {
	if !canKeyRe.MatchString(key) {
		return false
	}
	f := strings.Split(key, ":")
	start, _ := strconv.Atoi(f[1])
	n, _ := strconv.Atoi(f[2])
	return n >= 1 && n <= 32 && start <= 63 && (f[3] == "be" || start+n <= 64)
}

// gatewayEdgeDevices loads a gateway's devices with their profile (template) points.
func (s *server) gatewayEdgeDevices(r *http.Request, tenant, gw string) ([]edgeDev, error) {
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT d.id, d.profile, d.config, COALESCE(p.points,'[]'::jsonb)
		 FROM devices d LEFT JOIN device_profiles p ON p.id = d.config->>'device_profile_id' AND p.tenant_id=d.tenant_id
		 WHERE d.gateway_id=$1 AND d.tenant_id=$2 ORDER BY d.created_at`, gw, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var devs []edgeDev
	for rows.Next() {
		var d edgeDev
		var cfg, pts []byte
		if err := rows.Scan(&d.ID, &d.Profile, &cfg, &pts); err != nil {
			return nil, err
		}
		var c struct {
			Connection map[string]any `json:"connection"`
		}
		json.Unmarshal(cfg, &c)
		d.Conn = c.Connection
		if d.Conn == nil {
			d.Conn = map[string]any{}
		}
		json.Unmarshal(pts, &d.Points)
		devs = append(devs, d)
	}
	return devs, nil
}
