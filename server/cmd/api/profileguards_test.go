package main

import (
	"encoding/json"
	"math"
	"net/http"
	"testing"
)

func TestProfileGuards(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{"id": "p", "register": 2.0, "func": 4.0, "type": "f32", "scale": 1.0, "min": 0.0, "max": 100.0}
	}
	for _, x := range []struct {
		k string
		v any
	}{{"register", -1.0}, {"register", 1.5}, {"register", 65535.0}, {"register", math.NaN()}, {"func", 3.5}, {"func", 0.0}, {"scale", 0.0}, {"scale", math.Inf(1)}, {"min", math.NaN()}, {"max", 0.0}, {"type", "bool"}} {
		p := base()
		p[x.k] = x.v
		if validateProfilePointsFor("modbus-generic", []map[string]any{p}) == nil {
			t.Fatal("accepted", x)
		}
	}
	p := base()
	q := base()
	q["id"] = "q"
	q["register"] = 3.0
	if validateProfilePointsFor("modbus-tcp", []map[string]any{p, q}) == nil {
		t.Fatal("overlap")
	}
	q["register"] = 4.0
	if e := validateProfilePointsFor("modbus-tcp", []map[string]any{p, q}); e != nil {
		t.Fatal(e)
	}
	q["id"] = "p"
	if validateProfilePointsFor("modbus-generic", []map[string]any{p, q}) == nil {
		t.Fatal("duplicate")
	}
	for _, fn := range []float64{1, 2} {
		p := base()
		p["func"] = fn
		p["type"] = "u16"
		if validateProfilePointsFor("modbus-generic", []map[string]any{p}) == nil {
			t.Fatal("coil numeric")
		}
		delete(p, "type")
		if e := validateProfilePointsFor("modbus-generic", []map[string]any{p}); e != nil {
			t.Fatal("legacy bool", e)
		}
	}
	// Same address in separate Modbus spaces is not overlap; optional legacy defaults remain.
	p = map[string]any{"id": "legacy", "register": 0.0}
	if e := validateProfilePointsFor("modbus-generic", []map[string]any{p}); e != nil {
		t.Fatal(e)
	}
	q = map[string]any{"id": "holding", "register": 0.0, "func": 3.0}
	if e := validateProfilePointsFor("modbus-generic", []map[string]any{p, q}); e != nil {
		t.Fatal(e)
	}
	if validateProfilePointsFor("snmp", []map[string]any{{"id": "p", "oid": ".1.3.6.1.2.1.1.3.0", "min": 0.0, "max": 10.0}, {"id": "p", "oid": ".1.3.6.1.2.1.1.4.0", "min": 0.0, "max": 10.0}}) == nil {
		t.Fatal("duplicate nonmodbus")
	}
}
func TestIntegrationProfileGuardRejectsBeforeStorage(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-pguard")
	m := http.NewServeMux()
	m.HandleFunc("POST /v1/profiles", s.createProfile)
	for _, points := range []string{`[{"id":"p","register":1.5}]`, `[{"id":"p","register":0},{"id":"p","register":2}]`, `[{"id":"p","register":65535,"type":"f32"}]`} {
		body := `{"name":"Rejected","driver_profile":"modbus-generic","points":` + points + `}`
		if w := call(m, "itest-pguard", "admin", "POST", "/v1/profiles", body); w.Code != 400 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
	var n int
	s.st.Pool.QueryRow(t.Context(), `SELECT count(*) FROM device_profiles WHERE tenant_id='itest-pguard'`).Scan(&n)
	if n != 0 {
		t.Fatal("invalid stored")
	}
	w := call(m, "itest-pguard", "admin", "POST", "/v1/profiles", `{"name":"Valid","driver_profile":"modbus-tcp","points":[{"id":"p","register":0,"type":"f32","min":0,"max":100}]}`)
	if w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var r struct{ ID string }
	json.Unmarshal(w.Body.Bytes(), &r)
	if r.ID == "" {
		t.Fatal("no id")
	}
}
