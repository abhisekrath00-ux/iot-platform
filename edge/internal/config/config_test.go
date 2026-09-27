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
