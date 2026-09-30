// Package config loads edge-agent configuration from a YAML file.
package config

import (
	"fmt"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/paths"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	GatewayID string `yaml:"gateway_id"`
	TenantID  string `yaml:"tenant_id"`
	Serial    string `yaml:"serial"` // gateway serial; fleet manifests are address-checked against it

	// ArtifactDir stages fleet release artifacts by digest (air-gapped
	// bundles pre-seed it). Defaults to /var/lib/hexmon-edge/artifacts.
	ArtifactDir string `yaml:"artifact_dir"`

	MQTT struct {
		Host     string `yaml:"host"`
		Port     int    `yaml:"port"`
		TLS      bool   `yaml:"tls"`
		CAFile   string `yaml:"ca_file"`
		CertFile string `yaml:"cert_file"`
		KeyFile  string `yaml:"key_file"`
		ClientID string `yaml:"client_id"`
	} `yaml:"mqtt"`

	QueuePath string `yaml:"queue_path"` // SQLite file for store-and-forward

	Devices []Device `yaml:"devices"`

	// Commands: only these actions may execute on this gateway.
	AllowedCommands []string `yaml:"allowed_commands"`
}

type Device struct {
	ID       string `yaml:"id"`
	Profile  string `yaml:"profile"` // e.g. "modbus-energy-meter", "door-contact"
	Port     string `yaml:"port"`    // e.g. /dev/ttyUSB0 (pin via udev)
	Baud     int    `yaml:"baud"`
	DataBits int    `yaml:"data_bits"`
	StopBits int    `yaml:"stop_bits"`
	Parity   string `yaml:"parity"` // none|odd|even
	Address  int    `yaml:"address"`
	// Network transports (modbus-tcp, opcua): no serial port involved.
	Host     string `yaml:"host"`
	NetPort  int    `yaml:"net_port"` // default 502 (modbus-tcp), 4840 (opcua)
	Endpoint string `yaml:"endpoint"` // opcua endpoint URL, e.g. opc.tcp://10.0.0.5:4840
	// OPC UA security. Secrets never live in the file: the password is read
	// from the environment variable named by password_env.
	Security    string        `yaml:"security"` // none (default) | sign | sign-and-encrypt
	ClientCert  string        `yaml:"client_cert"`
	ClientKey   string        `yaml:"client_key"`
	Username    string        `yaml:"username"`
	PasswordEnv string        `yaml:"password_env"`
	Interval    time.Duration `yaml:"interval"`
	Points      []Point       `yaml:"points"`
}

type Point struct {
	ID        string  `yaml:"id"`         // e.g. "kwh"
	Register  int     `yaml:"register"`   // protocol register / coil
	Func      int     `yaml:"func"`       // modbus function: 1 coils, 2 discrete, 3 holding, 4 input (default 4)
	Type      string  `yaml:"type"`       // u16|i16|u32|i32|f32|bool (default u16; bool for func 1/2)
	WordOrder string  `yaml:"word_order"` // abcd|badc|cdab|dcba for 32-bit types (default abcd)
	Scale     float64 `yaml:"scale"`
	Unit      string  `yaml:"unit"`
	Key       string  `yaml:"key"`     // serial-json: field name / key=value key / CSV column index
	NodeID    string  `yaml:"node_id"` // opcua: e.g. ns=2;s=Boiler.Temp
	Min       float64 `yaml:"min"`     // validation range
	Max       float64 `yaml:"max"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if c.GatewayID == "" || c.TenantID == "" {
		return nil, fmt.Errorf("gateway_id and tenant_id are required")
	}
	if c.QueuePath == "" {
		c.QueuePath = paths.Queue()
	}
	for _, d := range c.Devices {
		if d.Interval <= 0 {
			return nil, fmt.Errorf("device %s: interval required", d.ID)
		}
		for _, p := range d.Points {
			if p.Max <= p.Min {
				return nil, fmt.Errorf("device %s point %s: validation range required", d.ID, p.ID)
			}
		}
	}
	return &c, nil
}

// DefaultIdentityFiles fills blank mTLS paths from the directory that
// claim.SaveIdentity wrote, when those files exist. Explicit config wins.
func (c *Config) DefaultIdentityFiles(dir string) {
	fill := func(dst *string, name string) {
		if *dst != "" {
			return
		}
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			*dst = p
		}
	}
	fill(&c.MQTT.CAFile, "ca.pem")
	fill(&c.MQTT.CertFile, "identity.crt")
	fill(&c.MQTT.KeyFile, "identity.key")
}
