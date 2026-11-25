package setup

// Runtime mapping is review-only. It never writes files or activates a unit.
import (
	"bytes"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/claim"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"gopkg.in/yaml.v3"
)

type RuntimeMapping struct {
	ConfigYAML, Unit string
	CanStart         bool
	Message          string
}

func BuildRuntimeMapping(raw []byte, id *claim.Identity, serial, tenant, identityDir, brokerCAFile, brokerCAPEM, binary, configPath, stateDir, user string) (RuntimeMapping, error) {
	if e := ValidateIdentity(identityDir, serial); e != nil {
		return RuntimeMapping{}, e
	}
	brokerSaved, e := os.ReadFile(brokerCAFile)
	if e != nil || !bytes.Equal(brokerSaved, []byte(brokerCAPEM)) {
		return RuntimeMapping{}, fmt.Errorf("broker trust file differs from explicit transport trust")
	}
	return buildRuntimeMappingChecked(raw, id, serial, tenant, identityDir, brokerCAFile, brokerCAPEM, binary, configPath, stateDir, user)
}
func buildRuntimeMappingChecked(raw []byte, id *claim.Identity, serial, tenant, identityDir, brokerCAFile, brokerCAPEM, binary, configPath, stateDir, user string) (RuntimeMapping, error) {
	refuse := RuntimeMapping{}
	if id == nil || id.GatewayID == "" || serial == "" || tenant == "" || len(raw) > 2<<20 || !regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,30}$`).MatchString(user) || user == "root" {
		return refuse, fmt.Errorf("explicit accepted identity, trust, targets and non-root service user required")
	}
	// systemd performs its own expansion. Keep generated fields to a conservative
	// literal subset, including refusal of percent specifiers and newlines.
	for _, p := range []string{identityDir, brokerCAFile, binary, configPath, stateDir} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || !regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`).MatchString(p) {
			return refuse, fmt.Errorf("literal canonical runtime paths required")
		}
	}
	paths := []string{identityDir, brokerCAFile, binary, configPath, stateDir}
	for i, p := range paths {
		for _, q := range paths[:i] {
			for _, pair := range [][2]string{{p, q}, {q, p}} {
				rel, e := filepath.Rel(pair[0], pair[1])
				if e != nil || rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					return refuse, fmt.Errorf("runtime targets overlap")
				}
			}
		}
	}
	if identityDir == stateDir || configPath == binary || brokerCAFile == filepath.Join(identityDir, "device-ca.pem") {
		return refuse, fmt.Errorf("separate identity/runtime state and broker transport trust required")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(brokerCAPEM)) {
		return refuse, fmt.Errorf("explicit broker transport CA required")
	}
	var cfg config.Config
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if decoder.Decode(&cfg) != nil {
		return refuse, fmt.Errorf("strict server configuration required")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return refuse, fmt.Errorf("single server configuration required")
	}
	if cfg.GatewayID != id.GatewayID || cfg.TenantID != tenant || cfg.Serial != serial || !cfg.MQTT.TLS || cfg.MQTT.Host == "" || cfg.MQTT.Port < 1 || cfg.MQTT.Port > 65535 {
		return refuse, fmt.Errorf("TLS configuration must match accepted identity")
	}
	if ip := net.ParseIP(cfg.MQTT.Host); strings.EqualFold(cfg.MQTT.Host, "localhost") || ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		return refuse, fmt.Errorf("reachable non-loopback TLS broker required")
	}
	if cfg.Autodetect.Enabled == nil || *cfg.Autodetect.Enabled || cfg.Autodetect.AutoAdd || cfg.ManagedDevices || cfg.RemoteRestart || cfg.AllowAutomaticCommands || len(cfg.AllowedCommands) > 0 || len(cfg.Outputs) > 0 || len(cfg.Rules) > 0 {
		return refuse, fmt.Errorf("unapproved discovery or actuation refused")
	}
	if cfg.MQTT.CAFile != "" || cfg.MQTT.CertFile != "" || cfg.MQTT.KeyFile != "" || cfg.QueuePath != "" || cfg.ArtifactDir != "" || cfg.UI.Listen != "" {
		return refuse, fmt.Errorf("server must not choose local runtime paths")
	}
	for _, d := range cfg.Devices {
		if len(d.Writes) > 0 {
			return refuse, fmt.Errorf("device writes refused")
		}
	}
	cfg.MQTT.CAFile = brokerCAFile
	cfg.MQTT.CertFile = filepath.Join(identityDir, "identity.crt")
	cfg.MQTT.KeyFile = filepath.Join(identityDir, "identity.key")
	cfg.QueuePath = filepath.Join(stateDir, "queue.db")
	cfg.ArtifactDir = filepath.Join(stateDir, "artifacts")
	cfg.UI.Listen = "127.0.0.1:8088"
	mapped, e := yaml.Marshal(cfg)
	if e != nil {
		return refuse, e
	}
	args := " -config " + configPath + " -identity-dir " + identityDir
	unit := strings.Join([]string{"[Unit]", "Description=HexThings Edge, explicit activation required", "After=network-online.target", "Wants=network-online.target", "[Service]", "User=" + user, "Group=" + user, "ExecStartPre=" + binary + " -check-config" + args, "ExecStart=" + binary + args, "Restart=on-failure", "NoNewPrivileges=true", "ProtectSystem=strict", "ProtectHome=true", "PrivateTmp=true", "ReadOnlyPaths=" + identityDir + " " + brokerCAFile + " " + configPath, "ReadWritePaths=" + stateDir, ""}, "\n")
	// No Install section: generation cannot enable boot startup.
	return RuntimeMapping{ConfigYAML: string(mapped), Unit: unit, CanStart: false, Message: "Mapping only. File installation, service-account access, current authorization and real broker connectivity remain unverified. No unit was installed or enabled."}, nil
}
