package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestValidateScan(t *testing.T) {
	ok := map[string]scanParams{
		"modbus-rtu": {Port: "/dev/ttyUSB0", Baud: 9600},
		"lan":        {CIDR: "192.168.1.0/24"},
		"bacnet":     {Broadcast: "192.168.1.255"},
	}
	for k, p := range ok {
		if err := validateScan(k, &p); err != nil {
			t.Errorf("%s: %v", k, err)
		}
		if k == "modbus-rtu" && (p.From != 1 || p.To != 32 || p.DataBits != 8 || p.Parity != "none") {
			t.Errorf("defaults %+v", p)
		}
	}
	bad := map[string]scanParams{
		"modbus-rtu": {Port: "/etc/passwd", Baud: 9600},
		"lan":        {CIDR: "8.8.8.0/24"},
		"bacnet":     {Broadcast: "not-an-ip"},
		"exec":       {},
	}
	for k, p := range bad {
		kind := k
		if k == "exec" {
			kind = "exec"
		}
		if validateScan(kind, &p) == nil {
			t.Errorf("%s accepted", k)
		}
	}
	for _, p := range []scanParams{{Port: "/dev/ttyUSB0", Baud: 12345}, {Port: "/dev/ttyUSB0", Baud: 9600, From: 5, To: 2}, {Port: "/dev/ttyUSB0", Baud: 9600, To: 300}, {CIDR: "10.0.0.0/8"}} {
		k := "modbus-rtu"
		if p.CIDR != "" {
			k = "lan"
		}
		if validateScan(k, &p) == nil {
			t.Errorf("accepted %+v", p)
		}
	}
}

func TestConnectionFromScan(t *testing.T) {
	rtu := []byte(`{"slaves":[{"address":5},{"address":9}]}`)
	if !connectionFromScan("modbus-rtu", rtu, map[string]any{"address": 5.0}) || connectionFromScan("modbus-rtu", rtu, map[string]any{"address": 6.0}) {
		t.Error("rtu membership")
	}
	lan := []byte(`{"hosts":[{"Addr":"10.0.0.5","Port":502}]}`)
	if !connectionFromScan("lan", lan, map[string]any{"host": "10.0.0.5", "net_port": 502.0}) || connectionFromScan("lan", lan, map[string]any{"host": "10.0.0.5", "net_port": 4840.0}) || connectionFromScan("lan", lan, map[string]any{"host": "10.0.0.6"}) {
		t.Error("lan membership")
	}
	if connectionFromScan("lan", []byte(`junk`), map[string]any{"host": "x"}) {
		t.Error("junk accepted")
	}
}

func TestIntegrationScanAddDevice(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-sc1")
	t.Cleanup(func() {
		s.st.Pool.Exec(t.Context(), `DELETE FROM points WHERE device_id IN (SELECT id FROM devices WHERE tenant_id='itest-sc1' AND config ? 'added_from_scan')`)
		s.st.Pool.Exec(t.Context(), `DELETE FROM devices WHERE tenant_id='itest-sc1' AND config ? 'added_from_scan'`)
		s.st.Pool.Exec(t.Context(), `DELETE FROM gateway_scans WHERE tenant_id='itest-sc1'`)
		s.st.Pool.Exec(t.Context(), `DELETE FROM device_profiles WHERE tenant_id='itest-sc1'`)
	})
	ctx := t.Context()
	s.st.Pool.Exec(ctx, `INSERT INTO device_profiles(id,tenant_id,name,driver_profile,points,created_by) VALUES('itest-sc1-p','itest-sc1','Selec test','modbus-generic','[{"id":"v","register":0,"func":4,"type":"u16","min":0,"max":300}]','x')`)
	s.st.Pool.Exec(ctx, `INSERT INTO gateway_scans(id,tenant_id,gateway_id,kind,params,status,result) VALUES('itest-sc1-s','itest-sc1','itest-sc1-gw','modbus-rtu','{}','done','{"ok":true,"slaves":[{"address":5,"matches":[]}]}'),('itest-sc1-run','itest-sc1','itest-sc1-gw','lan','{}','requested',NULL)`)
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/gateways/{id}/scans", s.requestScan)
	api.HandleFunc("GET /v1/gateways/{id}/scans", s.listScans)
	api.HandleFunc("POST /v1/gateways/{id}/scans/{sid}/add", s.addScannedDevice)
	add := func(role, sid, body string) int {
		return call(api, "itest-sc1", role, "POST", "/v1/gateways/itest-sc1-gw/scans/"+sid+"/add", body).Code
	}
	good := `{"profile_id":"itest-sc1-p","device_name":"Meter 5","connection":{"port":"/dev/ttyUSB0","baud":9600,"address":5,"interval_seconds":10}}`
	if c := add("viewer", "itest-sc1-s", good); c != 403 {
		t.Errorf("viewer add: %d", c)
	}
	if c := add("operator", "itest-sc1-run", good); c != 404 {
		t.Errorf("unfinished scan: %d", c)
	}
	if c := add("operator", "itest-sc1-s", strings.Replace(good, `"address":5`, `"address":6`, 1)); c != 409 {
		t.Errorf("device not in scan: %d", c)
	}
	if c := add("operator", "itest-sc1-s", strings.Replace(good, "/dev/ttyUSB0", "/etc/passwd", 1)); c != 400 {
		t.Errorf("bad port: %d", c)
	}
	if c := add("operator", "itest-sc1-s", good); c != 201 {
		t.Fatalf("add: %d", c)
	}
	if c := add("operator", "itest-sc1-s", good); c != 409 {
		t.Errorf("duplicate add: %d", c)
	}
	cfg := call(api, "itest-sc1", "admin", "GET", "/v1/gateways/itest-sc1-gw/edge-config", "")
	_ = cfg
	if c := call(api, "itest-other", "admin", "POST", "/v1/gateways/itest-sc1-gw/scans/itest-sc1-s/add", good).Code; c != 404 {
		t.Errorf("cross-tenant add: %d", c)
	}
	// Validation happens before the gateway or broker is touched.
	if c := call(api, "itest-sc1", "operator", "POST", "/v1/gateways/itest-sc1-gw/scans", `{"kind":"lan","params":{"cidr":"8.8.8.0/24"}}`).Code; c != 400 {
		t.Errorf("public cidr: %d", c)
	}
	if c := call(api, "itest-sc1", "viewer", "POST", "/v1/gateways/itest-sc1-gw/scans", `{"kind":"lan","params":{"cidr":"192.168.1.0/24"}}`).Code; c != 403 {
		t.Errorf("viewer scan: %d", c)
	}
	w := call(api, "itest-sc1", "viewer", "GET", "/v1/gateways/itest-sc1-gw/scans", "")
	var list []map[string]any
	if json.Unmarshal(w.Body.Bytes(), &list); w.Code != 200 || len(list) != 2 {
		t.Errorf("list: %d %s", w.Code, w.Body.String())
	}
}
