package config

import (
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
)

// Issue is one finding from Lint. Errors would stop or break the agent;
// warnings are things an operator should look at.
type Issue struct {
	Error bool
	Msg   string
}

func (i Issue) String() string {
	if i.Error {
		return "ERROR: " + i.Msg
	}
	return "warning: " + i.Msg
}

var winCOM = regexp.MustCompile(`(?i)^(\\\\\.\\)?COM[0-9]+$`)

// LintPort checks a serial port name against an OS ("linux", "windows", "darwin").
// Returns "" when it looks right.
func LintPort(goos, port string) string {
	switch goos {
	case "windows":
		if !winCOM.MatchString(port) {
			return fmt.Sprintf("serial port %q is not a Windows name like COM3 (use \\\\.\\COM12 for COM10 and above)", port)
		}
	default:
		if !strings.HasPrefix(port, "/dev/") {
			return fmt.Sprintf("serial port %q is not a device path like /dev/ttyUSB0 (prefer a /dev/serial/by-id/... path so it survives re-plugging)", port)
		}
	}
	return ""
}

// Lint checks a loaded config for problems that Load does not catch. goos picks
// the serial-port naming rules. It reads files named by the config (TLS files)
// but never connects to anything.
func Lint(c *Config, goos string) []Issue {
	var out []Issue
	add := func(err bool, f string, a ...any) { out = append(out, Issue{Error: err, Msg: fmt.Sprintf(f, a...)}) }

	if c.MQTT.Host == "" {
		add(true, "mqtt.host is empty: the agent cannot reach a broker")
	}
	if c.MQTT.Port < 0 || c.MQTT.Port > 65535 {
		add(true, "mqtt.port %d is out of range", c.MQTT.Port)
	}
	if !c.MQTT.TLS && c.MQTT.Host != "" {
		if ip := net.ParseIP(c.MQTT.Host); c.MQTT.Host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			add(false, "mqtt.tls is off for a non-local broker: telemetry and commands cross the network unencrypted")
		}
	}
	if (c.MQTT.CertFile == "") != (c.MQTT.KeyFile == "") {
		add(true, "mqtt.cert_file and mqtt.key_file must be set together")
	}
	for name, f := range map[string]string{"mqtt.ca_file": c.MQTT.CAFile, "mqtt.cert_file": c.MQTT.CertFile, "mqtt.key_file": c.MQTT.KeyFile} {
		if f == "" {
			continue
		}
		if _, err := os.Stat(f); err != nil {
			add(true, "%s: %v", name, err)
		}
	}
	if l := c.UI.Listen; l != "" && l != "off" {
		host, _, err := net.SplitHostPort(l)
		if err != nil {
			add(true, "ui.listen %q is not host:port", l)
		} else if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			add(false, "ui.listen %q exposes the status page beyond this machine; firewall it", l)
		}
	}

	seen := map[string]bool{}
	for _, d := range c.Devices {
		if d.ID == "" {
			add(true, "a device has no id")
			continue
		}
		if seen[d.ID] {
			add(true, "device id %q is used twice", d.ID)
		}
		seen[d.ID] = true
		if d.Port != "" {
			if m := LintPort(goos, d.Port); m != "" {
				add(false, "device %s: %s", d.ID, m)
			}
			if d.Address < 0 || d.Address > 247 {
				add(true, "device %s: address %d is outside 0-247", d.ID, d.Address)
			}
		}
		pts := map[string]bool{}
		for _, p := range d.Points {
			if p.ID == "" {
				add(true, "device %s has a point with no id", d.ID)
			} else if pts[p.ID] {
				add(true, "device %s: point id %q is used twice", d.ID, p.ID)
			}
			pts[p.ID] = true
		}
		if len(d.Points) == 0 {
			add(false, "device %s has no points", d.ID)
		}
	}
	if c.CommandMode == "modbus" && len(c.AllowedCommands) == 0 {
		add(false, "command_mode is modbus but allowed_commands is empty, so every command is rejected")
	}
	return out
}

// HasError reports whether any issue is an error.
func HasError(is []Issue) bool {
	for _, i := range is {
		if i.Error {
			return true
		}
	}
	return false
}
