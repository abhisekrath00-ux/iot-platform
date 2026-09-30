package config

import (
	"os"
	"path/filepath"
	"testing"
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
