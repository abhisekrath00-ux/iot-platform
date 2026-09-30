package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestValidateConnection(t *testing.T) {
	ok := []struct {
		d string
		c map[string]any
	}{
		{"modbus-generic", map[string]any{"port": "/dev/ttyUSB0", "baud": 9600.0, "address": 3.0}},
		{"serial-json", map[string]any{"port": "COM3", "baud": 115200.0}},
		{"modbus-tcp", map[string]any{"host": "192.168.1.50", "net_port": 502.0, "address": 1.0}},
		{"opcua", map[string]any{"endpoint": "opc.tcp://10.0.0.5:4840/ua"}},
	}
	for _, c := range ok {
		if err := validConnection(c.d, c.c); err != nil {
			t.Errorf("%s %v: %v", c.d, c.c, err)
		}
	}
	bad := []struct {
		d string
		c map[string]any
	}{
		{"modbus-generic", map[string]any{"port": "/etc/passwd"}},
		{"modbus-generic", map[string]any{"port": "/dev/ttyUSB0", "evil": "x"}},
		{"modbus-tcp", map[string]any{"host": "a b; rm -rf"}},
		{"opcua", map[string]any{"endpoint": "http://x"}},
		{"serial-json", map[string]any{"port": "COM3", "parity": "weird"}},
	}
	for _, c := range bad {
		if err := validConnection(c.d, c.c); err == nil {
			t.Errorf("%s %v should be rejected", c.d, c.c)
		}
	}
}

func TestValidatePointsPerDriver(t *testing.T) {
	good := []map[string]any{{"id": "temp", "node_id": "ns=2;s=Boiler.Temp", "min": 0.0, "max": 400.0}}
	if err := validateProfilePointsFor("opcua", good); err != nil {
		t.Fatal(err)
	}
	if err := validateProfilePointsFor("opcua", []map[string]any{{"id": "t", "node_id": "garbage", "min": 0.0, "max": 1.0}}); err == nil {
		t.Fatal("bad node id accepted")
	}
	if err := validateProfilePointsFor("serial-json", []map[string]any{{"id": "t", "key": "temp"}}); err == nil {
		t.Fatal("missing range accepted")
	}
}

func TestRenderEdgeYAMLParses(t *testing.T) {
	out := renderEdgeYAML("t1", "g1", "HX-1", []edgeDev{
		{ID: "d1", Profile: "modbus-tcp", Conn: map[string]any{"host": "10.0.0.2", "address": 1.0, "interval_seconds": 5.0},
			Points: []map[string]any{{"id": "temp", "register": 100.0, "func": 3.0, "type": "i16", "scale": 0.1, "unit": "C", "min": -40.0, "max": 150.0}}},
		{ID: "d2", Profile: "serial-json", Conn: map[string]any{"port": "COM3", "baud": 115200.0},
			Points: []map[string]any{{"id": "temp", "key": "temp", "min": 0.0, "max": 100.0}}},
	})
	var parsed struct {
		GatewayID string `yaml:"gateway_id"`
		Devices   []struct {
			ID       string `yaml:"id"`
			Profile  string `yaml:"profile"`
			Interval string `yaml:"interval"`
			Port     string `yaml:"port"`
			Baud     int    `yaml:"baud"`
			Points   []struct {
				ID  string  `yaml:"id"`
				Max float64 `yaml:"max"`
			} `yaml:"points"`
		} `yaml:"devices"`
	}
	if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid yaml: %v\n%s", err, out)
	}
	if parsed.GatewayID != "g1" || len(parsed.Devices) != 2 || parsed.Devices[0].Interval != "5s" ||
		parsed.Devices[1].Port != "COM3" || parsed.Devices[1].Baud != 115200 || parsed.Devices[0].Points[0].Max != 150 {
		t.Fatalf("unexpected parse: %+v\n%s", parsed, out)
	}
	if !strings.Contains(renderEdgeYAML("t", "g", "s", nil), "[]") {
		t.Fatal("empty device list must still be valid")
	}
}
