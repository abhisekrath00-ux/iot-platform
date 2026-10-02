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

func TestExtraDriverPointsAndConnection(t *testing.T) {
	rng := map[string]any{"min": 0.0, "max": 100.0}
	pt := func(kv map[string]any) []map[string]any {
		m := map[string]any{"id": "p1"}
		for k, v := range rng {
			m[k] = v
		}
		for k, v := range kv {
			m[k] = v
		}
		return []map[string]any{m}
	}
	ok := map[string][]map[string]any{
		"snmp":     pt(map[string]any{"oid": ".1.3.6.1.2.1.1.3.0"}),
		"iec104":   pt(map[string]any{"ioa": 100.0}),
		"dnp3":     pt(map[string]any{"key": "ai", "register": 3.0}),
		"bacnet":   pt(map[string]any{"key": "ai:1"}),
		"coap":     pt(map[string]any{"key": "sensors/temp#v"}),
		"iec61850": pt(map[string]any{"key": "LD0/MMXU1.TotW.mag.f"}),
	}
	for d, p := range ok {
		if err := validateProfilePointsFor(d, p); err != nil {
			t.Errorf("%s good point rejected: %v", d, err)
		}
	}
	bad := map[string][]map[string]any{
		"snmp":     pt(map[string]any{"oid": "sysUpTime"}),
		"iec104":   pt(map[string]any{"ioa": 0.0}),
		"dnp3":     pt(map[string]any{"key": "zz", "register": 1.0}),
		"bacnet":   pt(map[string]any{"key": "bad\x00"}),
		"iec61850": pt(map[string]any{"key": "nodomain"}),
		"coap":     {{"id": "p1", "key": "a"}}, // no range
	}
	for d, p := range bad {
		if err := validateProfilePointsFor(d, p); err == nil {
			t.Errorf("%s bad point accepted", d)
		}
	}
	if err := validConnection("snmp", map[string]any{"host": "10.0.0.9", "snmp_version": "3", "snmp_auth": "sha256", "snmp_auth_pass_env": "SNMP_AUTH"}); err != nil {
		t.Error(err)
	}
	for _, c := range []map[string]any{
		{"host": "h", "snmp_version": "1"}, {"host": "h", "snmp_auth": "md5"}, {"host": "h", "community_env": "public secret"},
	} {
		if validConnection("snmp", c) == nil {
			t.Errorf("accepted %v", c)
		}
	}
	if validConnection("bacnet", map[string]any{"host": "h", "community_env": "X"}) == nil {
		t.Error("snmp field accepted on bacnet")
	}
	y := renderEdgeYAML("t", "g", "s", []edgeDev{{ID: "d", Profile: "snmp", Conn: map[string]any{"host": "10.0.0.9", "snmp_version": "3"}, Points: ok["snmp"]}})
	if !strings.Contains(y, `snmp_version: "3"`) || !strings.Contains(y, `oid: ".1.3.6.1.2.1.1.3.0"`) {
		t.Errorf("yaml missing fields:\n%s", y)
	}
}
