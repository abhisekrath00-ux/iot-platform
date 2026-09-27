package fleetctl

import (
	"os"
	"path/filepath"
	"testing"
)

const goodYAML = `gateway_id: gw-1
tenant_id: demo
serial: AXON-0001
mqtt:
  host: localhost
  port: 1883
queue_path: /tmp/q.db
devices:
  - id: meter-1
    profile: modbus-generic
    port: /dev/ttyUSB0
    baud: 9600
    data_bits: 8
    stop_bits: 1
    parity: none
    address: 1
    interval: 5s
    points: [{id: kwh, register: 0, min: 0, max: 1000000}]
`

const oldYAML = `gateway_id: gw-1
tenant_id: demo
mqtt:
  host: localhost
  port: 1883
queue_path: /tmp/q.db
devices: []
`

func TestApplyHappyPath(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "edge-agent.yaml")
	artPath := filepath.Join(dir, "artifact")
	os.WriteFile(cfgPath, []byte(oldYAML), 0644)
	os.WriteFile(artPath, []byte(goodYAML), 0644)

	res := ApplyConfig(artPath, cfgPath)
	if !res.Applied || res.Devices != 1 {
		t.Fatalf("apply: %+v", res)
	}
	got, _ := os.ReadFile(cfgPath)
	if string(got) != goodYAML {
		t.Fatal("config not replaced")
	}
	// rollback restores the original byte-for-byte
	if err := Rollback(cfgPath, res.BackupPath); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(cfgPath)
	if string(got) != oldYAML {
		t.Fatal("rollback did not restore")
	}
}

func TestApplyInvalidArtifactKeepsOld(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "edge-agent.yaml")
	artPath := filepath.Join(dir, "artifact")
	os.WriteFile(cfgPath, []byte(oldYAML), 0644)
	os.WriteFile(artPath, []byte("gateway_id: ''\ntenant_id: ''\n"), 0644)

	res := ApplyConfig(artPath, cfgPath)
	if res.Applied || res.Err == nil {
		t.Fatalf("invalid artifact applied: %+v", res)
	}
	got, _ := os.ReadFile(cfgPath)
	if string(got) != oldYAML {
		t.Fatal("old config lost on failed apply")
	}
}

func TestApplyGarbageYAML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "edge-agent.yaml")
	artPath := filepath.Join(dir, "artifact")
	os.WriteFile(cfgPath, []byte(oldYAML), 0644)
	os.WriteFile(artPath, []byte("{not: [valid"), 0644)
	res := ApplyConfig(artPath, cfgPath)
	if res.Applied {
		t.Fatal("garbage applied")
	}
	got, _ := os.ReadFile(cfgPath)
	if string(got) != oldYAML {
		t.Fatal("old config lost")
	}
}
