package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadValidates(t *testing.T) {
	dir := t.TempDir()
	good := `
gateway_id: g1
tenant_id: t1
devices:
  - id: meter-1
    profile: modbus-energy-meter
    port: /dev/ttyUSB0
    interval: 5s
    points:
      - id: kwh
        register: 0
        unit: kWh
        min: 0
        max: 1000000
`
	p := filepath.Join(dir, "ok.yaml")
	os.WriteFile(p, []byte(good), 0o600)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.QueuePath == "" {
		t.Fatal("default queue path not set")
	}

	bad := `gateway_id: ""
tenant_id: t1`
	p2 := filepath.Join(dir, "bad.yaml")
	os.WriteFile(p2, []byte(bad), 0o600)
	if _, err := Load(p2); err == nil {
		t.Fatal("expected error for missing gateway_id")
	}
}

func TestDefaultIdentityFiles(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"ca.pem", "identity.crt", "identity.key"} {
		os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600)
	}
	var c Config
	c.MQTT.CertFile = "/explicit.crt"
	c.DefaultIdentityFiles(dir)
	if c.MQTT.CertFile != "/explicit.crt" {
		t.Fatal("explicit path must win")
	}
	if c.MQTT.CAFile != filepath.Join(dir, "ca.pem") || c.MQTT.KeyFile != filepath.Join(dir, "identity.key") {
		t.Fatalf("not defaulted: %+v", c.MQTT)
	}
}

// The packaged example must always load: it is the first file users edit.
func TestPackagedExampleLoads(t *testing.T) {
	c, err := Load("../../packaging/edge-agent.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	profiles := map[string]bool{}
	for _, d := range c.Devices {
		profiles[d.Profile] = true
	}
	for _, p := range []string{"modbus-tcp", "opcua", "serial-json", "modbus-generic"} {
		if !profiles[p] {
			t.Errorf("example missing %s", p)
		}
	}
}

func TestLoadParsesServerRenderedRules(t *testing.T) {
	y := `gateway_id: "g1"
tenant_id: "t1"
serial: "S1"
devices:
  []
outputs:
  - name: "siren"
    class: alarm
    kind: gpio_file
    path: "/sys/class/gpio/gpio17/value"
    active_low: true
    max_on: 300s
rules:
  - id: "offline"
    type: link_down
    output: "siren"
    for: 30s
  - id: "hot"
    type: threshold
    output: "siren"
    for: 0s
    name: "Boiler hot"
    device: "d1"
    point: "temp"
    op: "\u003e"
    value: 90
    pattern: pulse
`
	p := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(p, []byte(y), 0o600)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Outputs) != 1 || c.Outputs[0].MaxOn != 300*time.Second || !c.Outputs[0].ActiveLow || c.Outputs[0].Class != "alarm" {
		t.Fatalf("%+v", c.Outputs)
	}
	if len(c.Rules) != 2 || c.Rules[0].For != 30*time.Second || c.Rules[1].Op != ">" || c.Rules[1].Value != 90 || c.Rules[1].Pattern != "pulse" {
		t.Fatalf("%+v", c.Rules)
	}
	lp := filepath.Join(t.TempDir(), "local-rules.yaml")
	if rs, err := LoadLocalRules(lp); err != nil || rs != nil {
		t.Fatalf("missing file: %v %v", rs, err)
	}
	os.WriteFile(lp, []byte("rules:\n  - id: l1\n    type: link_down\n    output: siren\n"), 0o600)
	if rs, err := LoadLocalRules(lp); err != nil || len(rs) != 1 {
		t.Fatalf("%v %v", rs, err)
	}
}
