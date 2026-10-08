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
	// FleetPublicKey (base64 Ed25519) makes the agent refuse fleet manifests the control plane did not sign.
	FleetPublicKey string `yaml:"fleet_public_key"`

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

	// Autodetect lets the gateway find devices by itself (read-only) and
	// propose them. See docs/edge-autodetect.md.
	Autodetect Autodetect `yaml:"autodetect"`

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
	// AllowAutomaticCommands lets the server's AUTOMATIC commands (raised without a human approver under
	// an admin's per-target setting) switch this gateway's alarm outputs (class alarm, kind modbus_coil)
	// on or off. Off by default: only this file can turn it on, never the server.
	AllowAutomaticCommands bool `yaml:"allow_automatic_commands"`
	// RemoteRestart lets an approved server request restart this agent process (it exits and the service
	// manager must start it again; the shipped Linux unit has Restart=always). Off by default; only this file
	// can turn it on. It never reboots the machine.
	RemoteRestart bool `yaml:"remote_restart"`
	// ManagedDevices lets the server push device templates (the device list only) into managed-devices.yaml. Local opt-in; connection, TLS and identity settings are never pushed.
	ManagedDevices bool   `yaml:"managed_devices"`
	CommandMode    string `yaml:"command_mode"` // "" = reject all commands | "simulate" = record only, no hardware
}

// Autodetect configures edge-side discovery. Everything defaults to on except
// auto_add: the agent proposes devices, a person confirms them in the dashboard
// (or here, with auto_add, for confident matches only).
type Autodetect struct {
	Enabled     *bool         `yaml:"enabled"`      // default true; false turns the whole thing off
	Interval    time.Duration `yaml:"interval"`     // between scans, default 6h, minimum 15m
	Serial      *bool         `yaml:"serial"`       // sweep serial ports (default true)
	LAN         *bool         `yaml:"lan"`          // probe the local private subnets (default true)
	BACnet      *bool         `yaml:"bacnet"`       // BACnet Who-Is broadcast (default true)
	SerialFrom  int           `yaml:"serial_from"`  // first slave address to try, default 1
	SerialTo    int           `yaml:"serial_to"`    // last slave address to try, default 32
	SerialPorts []string      `yaml:"serial_ports"` // extra ports to sweep besides the ones the OS lists (e.g. a custom udev name)
	LANCIDRs    []string      `yaml:"lan_cidrs"`    // explicit ranges instead of the local subnets
	AutoAdd     bool          `yaml:"auto_add"`     // add confident matches to polling on this gateway (default false)
}

func on(p *bool) bool { return p == nil || *p }

func (a Autodetect) IsEnabled() bool { return on(a.Enabled) }
func (a Autodetect) DoSerial() bool  { return on(a.Serial) }
func (a Autodetect) DoLAN() bool     { return on(a.LAN) }
func (a Autodetect) DoBACnet() bool  { return on(a.BACnet) }
func (a Autodetect) Every() time.Duration {
	if a.Interval <= 0 {
		return 6 * time.Hour
	}
	return a.Interval
}
func (a Autodetect) Range() (int, int) {
	f, t := a.SerialFrom, a.SerialTo
	if f == 0 {
		f = 1
	}
	if t == 0 {
		t = 32
	}
	return f, t
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
	if a := c.Autodetect; a.Interval != 0 && a.Interval < 15*time.Minute {
		return nil, fmt.Errorf("autodetect.interval must be at least 15m")
	}
	f, t := c.Autodetect.Range()
	if f < 1 || t > 247 || f > t {
		return nil, fmt.Errorf("autodetect serial_from/serial_to must be within 1-247")
	}
	if c.Autodetect.AutoAdd {
		c.mergeOverlay()
	}
	if c.ManagedDevices {
		if err := c.mergeManaged(ManagedPath()); err != nil {
			return nil, err
		}
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

// OverlayPath is where auto-added devices are kept, apart from the operator's
// own config file so a person never has their file rewritten by the agent.
func OverlayPath() string { return filepath.Join(paths.Data(), "autodetected-devices.yaml") }

type overlayFile struct {
	Devices []Device `yaml:"devices"`
}

// LoadOverlay reads the auto-added devices. A missing file is not an error.
func LoadOverlay(path string) ([]Device, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var o overlayFile
	if err := yaml.Unmarshal(b, &o); err != nil {
		return nil, err
	}
	return o.Devices, nil
}

// SaveOverlay writes the overlay atomically.
func SaveOverlay(path string, devs []Device) error {
	b, err := yaml.Marshal(overlayFile{Devices: devs})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append([]byte("# written by the edge agent's auto-add; delete a device here to stop polling it\n"), b...), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// mergeOverlay appends overlay devices that do not collide with configured ones
// (same id, or same serial port and address). Only used when auto_add is on.
func (c *Config) mergeOverlay() {
	devs, err := LoadOverlay(OverlayPath())
	if err != nil {
		return
	}
	for _, o := range devs {
		dup := false
		for _, d := range c.Devices {
			if d.ID == o.ID || (o.Port != "" && d.Port == o.Port && d.Address == o.Address) {
				dup = true
			}
		}
		if !dup && o.Interval > 0 {
			c.Devices = append(c.Devices, o)
		}
	}
}

// ManagedPath is where server-pushed device templates are stored.
func ManagedPath() string { return filepath.Join(paths.Data(), "managed-devices.yaml") }

// mergeManaged applies the server-pushed device list: a managed device replaces a local one with the same
// id, the rest are added. A missing file is fine; a broken file is an error so a bad push is never half used.
func (c *Config) mergeManaged(path string) error {
	devs, err := LoadOverlay(path)
	if err != nil {
		return fmt.Errorf("managed devices: %w", err)
	}
	for _, m := range devs {
		replaced := false
		for i, d := range c.Devices {
			if d.ID == m.ID {
				c.Devices[i], replaced = m, true
			}
		}
		if !replaced {
			c.Devices = append(c.Devices, m)
		}
	}
	return nil
}
