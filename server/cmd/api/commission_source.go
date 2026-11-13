package main

import (
	"encoding/json"
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"net/http"
)

// commissionSource returns only the assigned source connection, never arbitrary
// device.config contents. Missing or invalid records are explicit errors instead
// of allowing a resumed page to silently substitute examples.
func (s *server) commissionSource(r *http.Request, gateway string, device *string) (any, error) {
	if device == nil {
		return nil, nil
	}
	var name, driver string
	var raw []byte
	if err := s.st.Pool.QueryRow(r.Context(), `SELECT name,profile,config FROM devices WHERE id=$1 AND tenant_id=$2 AND gateway_id=$3`, *device, auth.Tenant(r), gateway).Scan(&name, &driver, &raw); err != nil {
		return nil, fmt.Errorf("saved source unavailable: %w", err)
	}
	var cfg struct {
		Connection map[string]any `json:"connection"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("saved source invalid: %w", err)
	}
	safe := map[string]any{}
	for _, key := range []string{"port", "baud", "data_bits", "stop_bits", "parity", "address", "host", "net_port", "endpoint", "interval_seconds", "snmp_version", "community_env", "snmp_auth", "snmp_auth_pass_env", "snmp_priv", "snmp_priv_pass_env"} {
		if value, ok := cfg.Connection[key]; ok {
			safe[key] = value
		}
	}
	return map[string]any{"device_id": *device, "name": name, "driver": driver, "connection": safe}, nil
}
