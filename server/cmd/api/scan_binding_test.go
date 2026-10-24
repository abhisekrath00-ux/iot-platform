package main

import (
	"testing"
)

func TestScanBindingTransport(t *testing.T) {
	p := []byte(`{"port":"/dev/ttyUSB0","baud":19200,"parity":"even","data_bits":8,"stop_bits":1}`)
	r := []byte(`{"slaves":[{"address":5}]}`)
	good := func() map[string]any {
		return map[string]any{"port": "/dev/ttyUSB0", "baud": 19200.0, "parity": "even", "address": 5.0}
	}
	if !boundScanConnection("modbus-rtu", "modbus-generic", p, r, good()) {
		t.Fatal("valid serial")
	}
	for _, tc := range []struct {
		k string
		v any
	}{{"port", "/dev/ttyUSB1"}, {"baud", 9600.0}, {"baud", 19200.5}, {"data_bits", 7.0}, {"stop_bits", 2.0}, {"parity", "odd"}, {"host", "10.0.0.5"}} {
		c := good()
		c[tc.k] = tc.v
		if boundScanConnection("modbus-rtu", "modbus-generic", p, r, c) {
			t.Fatal(tc)
		}
	}
	if boundScanConnection("modbus-rtu", "modbus-generic", []byte(`{}`), r, good()) {
		t.Fatal("legacy settings accepted")
	}
	if boundScanConnection("modbus-rtu", "opcua", p, r, good()) {
		t.Fatal("wrong driver")
	}
	lr := []byte(`{"hosts":[{"Addr":"10.0.0.5","Port":4840}]}`)
	c := map[string]any{"host": "10.0.0.5", "net_port": 4840.0, "endpoint": "opc.tcp://10.0.0.5:4840"}
	if !boundScanConnection("lan", "opcua", nil, lr, c) {
		t.Fatal("valid opc")
	}
	c["endpoint"] = "opc.tcp://10.0.0.6:4840"
	if boundScanConnection("lan", "opcua", nil, lr, c) {
		t.Fatal("endpoint bypass")
	}
	delete(c, "endpoint")
	delete(c, "net_port")
	if boundScanConnection("lan", "opcua", nil, lr, c) {
		t.Fatal("missing port")
	}
}

func TestScanBindingBACnetAndOptionalFields(t *testing.T) {
	r := []byte(`{"bacnet":[{"Addr":"192.168.1.9","Instance":42}]}`)
	c := map[string]any{"host": "192.168.1.9"}
	if !boundScanConnection("bacnet", "bacnet", nil, r, c) {
		t.Fatal("valid BACnet")
	}
	if boundScanConnection("bacnet", "snmp", nil, r, c) {
		t.Fatal("BACnet with unrelated driver")
	}
	c["host"] = "192.168.1.8"
	if boundScanConnection("bacnet", "bacnet", nil, r, c) {
		t.Fatal("unseen host")
	}
	p := []byte(`{"port":"/dev/ttyUSB0","baud":9600,"parity":"none","data_bits":8,"stop_bits":1}`)
	sr := []byte(`{"slaves":[{"address":5}]}`)
	sc := map[string]any{"port": "/dev/ttyUSB0", "address": 5.0}
	if !boundScanConnection("modbus-rtu", "modbus-generic", p, sr, sc) {
		t.Fatal("matching effective defaults")
	}
	sc["address"] = 5.5
	if boundScanConnection("modbus-rtu", "modbus-generic", p, sr, sc) {
		t.Fatal("fractional address")
	}
}
