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

	// UI is the read-only local status page. Empty means 127.0.0.1:8088;
	// "off" disables it. Non-loopback binds are an explicit operator choice.
	UI struct {
		Listen string `yaml:"listen"`
	} `yaml:"ui"`

	QueuePath string `yaml:"queue_path"` // SQLite file for store-and-forward

	Devices []Device `yaml:"devices"`

	// Outputs is the per-gateway allowlist of physical outputs that local
	// rules may drive (siren, buzzer, beacon). Rules refer to outputs by name
	// and can drive nothing else. See docs/edge-rules.md.
	Outputs []Output `yaml:"outputs"`
	// Rules run on the edge box itself, with or without the server. Rules
	// from the server ride in this file (config download or fleet config
	// artifact); the operator can add more in local-rules.yaml next to it.
	Rules []Rule `yaml:"rules"`

	// Commands: only these actions may execute on this gateway.
	AllowedCommands []string `yaml:"allowed_commands"`
	CommandMode     string   `yaml:"command_mode"` // "" = reject all commands | "simulate" = record only, no hardware
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
	ServerCert  string        `yaml:"server_cert"` // pinned OPC UA server certificate (PEM or DER); required for sign / sign-and-encrypt
	Username    string        `yaml:"username"`
	PasswordEnv string        `yaml:"password_env"`
	Interval    time.Duration `yaml:"interval"`
	Points      []Point       `yaml:"points"`
	// SNMP (profile "snmp"). Version "2c" (default) or "3". Secrets come from
	// the environment variables named here, never from the file.
	SNMPVersion  string `yaml:"snmp_version"`
	CommunityEnv string `yaml:"community_env"` // v2c community string
	AuthProto    string `yaml:"snmp_auth"`     // v3: sha|sha256|sha512 (md5 refused)
	AuthPassEnv  string `yaml:"snmp_auth_pass_env"`
	PrivProto    string `yaml:"snmp_priv"` // v3: aes|aes256 (des refused)
	PrivPassEnv  string `yaml:"snmp_priv_pass_env"`
	// Writes is the explicit allowlist of writable registers/coils for this
	// device. Nothing outside it can ever be written. Each entry needs a finite
	// min < max range, enforced before any frame is sent. Func: 5 coil,
	// 6 single register, 16 multiple registers.
	Writes []Point `yaml:"writes"`
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
	IOA       int     `yaml:"ioa"`     // iec104: information object address (1..16777215)
	OID       string  `yaml:"oid"`     // snmp: numeric OID, e.g. .1.3.6.1.2.1.1.3.0
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

// Output is one allowlisted annunciator. Only class "alarm" outputs may be
// driven by local rules; anything that moves process equipment stays on the
// approval + four-eyes command path.
type Output struct {
	Name  string `yaml:"name"`
	Class string `yaml:"class"` // must be "alarm"
	Kind  string `yaml:"kind"`  // gpio_file | modbus_coil | simulate
	// gpio_file: absolute path written with 1/0 (e.g. /sys/class/gpio/gpio17/value,
	// /sys/class/leds/siren/brightness). active_low inverts it.
	Path      string `yaml:"path"`
	ActiveLow bool   `yaml:"active_low"`
	// modbus_coil: a write point (id) of a device whose `writes` allowlist
	// has it with min 0 and max 1.
	Device string `yaml:"device"`
	Point  string `yaml:"point"`
	// MaxOn caps how long a siren can sound continuously (default 10m, max 1h);
	// after that it goes quiet until the alarm clears and re-triggers.
	MaxOn time.Duration `yaml:"max_on"`
}

// Rule is one edge-local alarm rule.
type Rule struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
	// Type: link_down (broker connection lost), threshold (device point vs
	// value), stale (device not read for `for`).
	Type    string        `yaml:"type"`
	For     time.Duration `yaml:"for"` // hold time before the rule fires (stale: the staleness window)
	Device  string        `yaml:"device"`
	Point   string        `yaml:"point"`
	Op      string        `yaml:"op"` // > >= < <= == !=
	Value   float64       `yaml:"value"`
	Output  string        `yaml:"output"`
	Pattern string        `yaml:"pattern"` // steady (default) | pulse
}

// LoadLocalRules reads operator-added rules (rules: [...]) from a file next to
// the main config. A missing file means no local rules.
func LoadLocalRules(path string) ([]Rule, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f struct {
		Rules []Rule `yaml:"rules"`
	}
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return f.Rules, nil
}
