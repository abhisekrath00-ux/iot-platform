package main

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
)

// Fixture is the actual JSON downloaded from the synthetic edge explorer's
// reviewed export, not a hand-retyped approximation of that format.
func TestIntegrationExplorerExportStoresProfileOnly(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-explorer-template")
	ctx := t.Context()
	var before int
	if err := s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM devices WHERE tenant_id=$1`, "itest-explorer-template").Scan(&before); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile("testdata/explorer-profile.json")
	if err != nil {
		t.Fatal(err)
	}
	m := http.NewServeMux()
	m.HandleFunc("POST /v1/profiles", s.createProfile)
	m.HandleFunc("GET /v1/profiles", s.listProfiles)
	w := call(m, "itest-explorer-template", "admin", "POST", "/v1/profiles", string(payload))
	if w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	w = call(m, "itest-explorer-template", "admin", "GET", "/v1/profiles", "")
	var rows []struct {
		Name     string           `json:"name"`
		Driver   string           `json:"driver_profile"`
		Points   []map[string]any `json:"points"`
		Defaults map[string]any   `json:"connection_defaults"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &rows) != nil || len(rows) != 1 {
		t.Fatal(w.Code, w.Body)
	}
	p := rows[0]
	if p.Name != "Synthetic temperature reviewed" || p.Driver != "modbus-generic" || len(p.Points) != 1 || len(p.Defaults) != 0 {
		t.Fatalf("wrong portable profile: %+v", p)
	}
	point := p.Points[0]
	for k, want := range map[string]any{"id": "temperature", "name": "Temperature", "description": "Register map: SYNTHETIC fixture map, p2", "register": float64(0), "func": float64(3), "type": "f32", "word_order": "abcd", "scale": float64(1), "unit": "C", "min": float64(-40), "max": float64(150)} {
		if point[k] != want {
			t.Fatalf("lost %s: %v wanted %v", k, point[k], want)
		}
	}
	var after int
	if err := s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM devices WHERE tenant_id=$1`, "itest-explorer-template").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("profile save assigned a device")
	}
}
