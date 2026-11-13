// Package setup fetches a safe starting configuration using the saved gateway
// identity. It does not enroll, start a service, or change an existing config.
package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/claim"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/fleetctl"
	"gopkg.in/yaml.v3"
)

type Result struct {
	GatewayID string `json:"gateway_id"`
	TenantID  string `json:"tenant_id"`
	Serial    string `json:"serial"`
	YAML      string `json:"config_yaml"`
	Signed    bool   `json:"signed_source_updates_available"`
}

func Fetch(ctx context.Context, client *http.Client, server string, id *claim.Identity, serial string, allowManaged bool) ([]byte, error) {
	u, err := url.Parse(server)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("use the server's HTTPS address without a path, query or login")
	}
	ip := net.ParseIP(u.Hostname())
	loop := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loop) {
		return nil, fmt.Errorf("server connection must use HTTPS; HTTP is permitted only for a local loopback test")
	}
	if id == nil || id.GatewayID == "" || id.IngestToken == "" || serial == "" {
		return nil, fmt.Errorf("saved gateway identity and serial are required; do not create another claim")
	}
	target := strings.TrimRight(u.String(), "/") + "/v1/edge/self/setup"
	req, err := http.NewRequestWithContext(ctx, "GET", target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+id.IngestToken)
	// Refuse even same-origin redirects: the approved server endpoint must answer
	// directly, so credentials can never migrate to a login or another host.
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("server connection check failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("server setup unavailable (HTTP %d); no local configuration was changed", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		return nil, fmt.Errorf("server setup response unreadable or too large")
	}
	var out Result
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("invalid server setup response")
	}
	if out.GatewayID != id.GatewayID || out.Serial != serial || out.TenantID == "" {
		return nil, fmt.Errorf("server setup does not match this gateway identity and serial")
	}
	var cfg config.Config
	if err := yaml.Unmarshal([]byte(out.YAML), &cfg); err != nil {
		return nil, fmt.Errorf("invalid server configuration")
	}
	if cfg.GatewayID != id.GatewayID || cfg.TenantID != out.TenantID || cfg.Serial != serial {
		return nil, fmt.Errorf("configuration identity does not match")
	}
	ip = net.ParseIP(cfg.MQTT.Host)
	if !cfg.MQTT.TLS || cfg.MQTT.Host == "" || strings.EqualFold(cfg.MQTT.Host, "localhost") || (ip != nil && (ip.IsLoopback() || ip.IsUnspecified())) || cfg.MQTT.Port < 1 || cfg.MQTT.Port > 65535 {
		return nil, fmt.Errorf("server did not supply a reachable TLS broker")
	}
	if cfg.MQTT.CAFile != "" || cfg.MQTT.CertFile != "" || cfg.MQTT.KeyFile != "" || cfg.QueuePath != "" || cfg.ArtifactDir != "" || cfg.UI.Listen != "" {
		return nil, fmt.Errorf("server setup must not choose local files or status-page bindings")
	}
	if cfg.Autodetect.Enabled == nil || *cfg.Autodetect.Enabled || cfg.Autodetect.AutoAdd || cfg.RemoteRestart || cfg.AllowAutomaticCommands || len(cfg.AllowedCommands) > 0 || len(cfg.Outputs) > 0 || len(cfg.Rules) > 0 || cfg.ManagedDevices {
		return nil, fmt.Errorf("server setup requested unapproved discovery, commands, outputs or managed updates")
	}
	for _, d := range cfg.Devices {
		if len(d.Writes) > 0 {
			return nil, fmt.Errorf("server setup requested device writes")
		}
	}
	if allowManaged {
		if !out.Signed || cfg.FleetPublicKey == "" {
			return nil, fmt.Errorf("signed source updates are not configured on this server")
		}
		if _, err := fleetctl.ParsePublicKey(cfg.FleetPublicKey); err != nil {
			return nil, fmt.Errorf("server source-update public key invalid")
		}
		cfg.ManagedDevices = true
	}
	// Marshal the typed shape, dropping unknown fields instead of forwarding
	// unrecognized future security settings into a local configuration.
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	return b, nil
}
