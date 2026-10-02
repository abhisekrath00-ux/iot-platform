package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strings"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// Edge-local alarm rules. An admin authors outputs (siren, buzzer) and rules
// per gateway; they are rendered into that gateway's edge-config YAML and run
// on the edge box even when the server or network is down. Only annunciator
// ("alarm") outputs exist here: a rule can never operate process equipment,
// which stays on the approval + four-eyes command path.

type edgeOutput struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"` // gpio_file | simulate
	Path         string `json:"path,omitempty"`
	ActiveLow    bool   `json:"active_low,omitempty"`
	MaxOnSeconds int    `json:"max_on_seconds,omitempty"`
}

type edgeRule struct {
	ID         string  `json:"id"`
	Name       string  `json:"name,omitempty"`
	Type       string  `json:"type"` // link_down | threshold | stale
	ForSeconds int     `json:"for_seconds"`
	Device     string  `json:"device,omitempty"`
	Point      string  `json:"point,omitempty"`
	Op         string  `json:"op,omitempty"`
	Value      float64 `json:"value,omitempty"`
	Output     string  `json:"output"`
	Pattern    string  `json:"pattern,omitempty"`
}

var (
	edgeIDRe   = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,48}$`)
	edgePathRe = regexp.MustCompile(`^/(sys/class/(gpio|leds)|sys/devices|dev|run)/[A-Za-z0-9._/-]{1,120}$`)
)

func validateEdgeRules(outs []edgeOutput, rules []edgeRule, devices map[string]bool) error {
	if len(outs) > 16 || len(rules) > 64 {
		return fmt.Errorf("at most 16 outputs and 64 rules per gateway")
	}
	names := map[string]bool{}
	for _, o := range outs {
		if !edgeIDRe.MatchString(o.Name) || names[o.Name] {
			return fmt.Errorf("output %q: name must be unique, 1-48 of letters, digits, . _ -", o.Name)
		}
		names[o.Name] = true
		switch o.Kind {
		case "simulate":
		case "gpio_file":
			if !edgePathRe.MatchString(o.Path) || strings.Contains(o.Path, "..") {
				return fmt.Errorf("output %s: path must be under /sys/class/gpio, /sys/class/leds, /sys/devices, /dev or /run", o.Name)
			}
		default:
			return fmt.Errorf("output %s: kind must be gpio_file or simulate (Modbus coil outputs are set up on the gateway itself)", o.Name)
		}
		if o.MaxOnSeconds < 0 || o.MaxOnSeconds > 3600 {
			return fmt.Errorf("output %s: max_on_seconds must be 0-3600 (0 = default 600)", o.Name)
		}
	}
	ids := map[string]bool{}
	for _, r := range rules {
		if !edgeIDRe.MatchString(r.ID) || ids[r.ID] {
			return fmt.Errorf("rule %q: id must be unique, 1-48 of letters, digits, . _ -", r.ID)
		}
		ids[r.ID] = true
		if !names[r.Output] {
			return fmt.Errorf("rule %s: output %q is not defined", r.ID, r.Output)
		}
		if r.ForSeconds < 0 || r.ForSeconds > 86400 {
			return fmt.Errorf("rule %s: for_seconds must be 0-86400", r.ID)
		}
		if r.Pattern != "" && r.Pattern != "steady" && r.Pattern != "pulse" {
			return fmt.Errorf("rule %s: pattern must be steady or pulse", r.ID)
		}
		if len(r.Name) > 80 || strings.ContainsAny(r.Name, "\n\r\"'\\") {
			return fmt.Errorf("rule %s: name up to 80 characters without quotes or line breaks", r.ID)
		}
		switch r.Type {
		case "link_down":
		case "threshold", "stale":
			if !devices[r.Device] {
				return fmt.Errorf("rule %s: device %q is not on this gateway", r.ID, r.Device)
			}
			if r.Type == "stale" {
				if r.ForSeconds < 5 {
					return fmt.Errorf("rule %s: stale needs for_seconds >= 5", r.ID)
				}
				break
			}
			if !keyRe.MatchString(r.Point) || math.IsNaN(r.Value) || math.IsInf(r.Value, 0) {
				return fmt.Errorf("rule %s: threshold needs a point and a finite value", r.ID)
			}
			switch r.Op {
			case ">", ">=", "<", "<=", "==", "!=":
			default:
				return fmt.Errorf("rule %s: op must be one of > >= < <= == !=", r.ID)
			}
		default:
			return fmt.Errorf("rule %s: type must be link_down, threshold or stale", r.ID)
		}
	}
	return nil
}

func renderRulesYAML(outs []edgeOutput, rules []edgeRule) string {
	if len(outs) == 0 && len(rules) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("outputs:\n")
	for _, o := range outs {
		fmt.Fprintf(&b, "  - name: %s\n    class: alarm\n    kind: %s\n", q(o.Name), o.Kind)
		if o.Path != "" {
			fmt.Fprintf(&b, "    path: %s\n", q(o.Path))
		}
		if o.ActiveLow {
			b.WriteString("    active_low: true\n")
		}
		if o.MaxOnSeconds > 0 {
			fmt.Fprintf(&b, "    max_on: %ds\n", o.MaxOnSeconds)
		}
	}
	if len(outs) == 0 {
		b.WriteString("  []\n")
	}
	b.WriteString("rules:\n")
	for _, r := range rules {
		fmt.Fprintf(&b, "  - id: %s\n    type: %s\n    output: %s\n    for: %ds\n", q(r.ID), r.Type, q(r.Output), r.ForSeconds)
		if r.Name != "" {
			fmt.Fprintf(&b, "    name: %s\n", q(r.Name))
		}
		if r.Device != "" {
			fmt.Fprintf(&b, "    device: %s\n", q(r.Device))
		}
		if r.Type == "threshold" {
			fmt.Fprintf(&b, "    point: %s\n    op: %s\n    value: %v\n", q(r.Point), q(r.Op), r.Value)
		}
		if r.Pattern != "" {
			fmt.Fprintf(&b, "    pattern: %s\n", r.Pattern)
		}
	}
	if len(rules) == 0 {
		b.WriteString("  []\n")
	}
	return b.String()
}

func (s *server) loadEdgeRules(r *http.Request, gw string) (outs []edgeOutput, rules []edgeRule, found bool) {
	var o, ru []byte
	err := s.st.Pool.QueryRow(r.Context(), `SELECT outputs, rules FROM gateway_edge_rules WHERE gateway_id=$1 AND tenant_id=$2`, gw, auth.Tenant(r)).Scan(&o, &ru)
	if err != nil {
		return nil, nil, false
	}
	json.Unmarshal(o, &outs)
	json.Unmarshal(ru, &rules)
	return outs, rules, true
}

func (s *server) getEdgeRules(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	gw := r.PathValue("id")
	var n int
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM gateways WHERE id=$1 AND tenant_id=$2`, gw, auth.Tenant(r)).Scan(&n)
	if n == 0 {
		http.Error(w, "gateway not found", 404)
		return
	}
	outs, rules, _ := s.loadEdgeRules(r, gw)
	if outs == nil {
		outs = []edgeOutput{}
	}
	if rules == nil {
		rules = []edgeRule{}
	}
	writeJSON(w, 200, map[string]any{"outputs": outs, "rules": rules})
}

func (s *server) putEdgeRules(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	gw, tenant := r.PathValue("id"), auth.Tenant(r)
	var in struct {
		Outputs []edgeOutput `json:"outputs"`
		Rules   []edgeRule   `json:"rules"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		http.Error(w, "bad json: "+err.Error(), 400)
		return
	}
	rows, err := s.st.Pool.Query(r.Context(), `SELECT id FROM devices WHERE gateway_id=$1 AND tenant_id=$2`, gw, tenant)
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	devs := map[string]bool{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			devs[id] = true
		}
	}
	rows.Close()
	var n int
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM gateways WHERE id=$1 AND tenant_id=$2`, gw, tenant).Scan(&n)
	if n == 0 {
		http.Error(w, "gateway not found", 404)
		return
	}
	if err := validateEdgeRules(in.Outputs, in.Rules, devs); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	ob, _ := json.Marshal(in.Outputs)
	rb, _ := json.Marshal(in.Rules)
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO gateway_edge_rules(gateway_id,tenant_id,outputs,rules,updated_by) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT (gateway_id) DO UPDATE SET outputs=$3, rules=$4, updated_by=$5, updated_at=now()`, gw, tenant, ob, rb, auth.User(r)); err != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "gateway.edge_rules.update", gw, map[string]any{"outputs": len(in.Outputs), "rules": len(in.Rules)})
	writeJSON(w, 200, map[string]any{"ok": true, "note": "download the gateway's edge-config again (or push it as a fleet config) and restart the agent to apply"})
}

// listGateways is the gateway picker for edge-rule and config screens.
func (s *server) listGateways(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT id, serial, status, kind, site_id FROM gateways WHERE tenant_id=$1 AND status<>'revoked' ORDER BY created_at DESC LIMIT 500`, auth.Tenant(r))
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, serial, status, kind, site string
		if rows.Scan(&id, &serial, &status, &kind, &site) == nil {
			out = append(out, map[string]any{"id": id, "serial": serial, "status": status, "kind": kind, "site_id": site})
		}
	}
	writeJSON(w, 200, out)
}
