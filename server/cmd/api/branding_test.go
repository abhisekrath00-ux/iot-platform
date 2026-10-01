package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestContrastWithWhite(t *testing.T) {
	if c := contrastWithWhite("#000000"); c < 20 {
		t.Fatalf("black %v", c)
	}
	if c := contrastWithWhite("#ffffff"); c > 1.1 {
		t.Fatalf("white %v", c)
	}
	if c := contrastWithWhite("#0071e3"); c < 4.5 {
		t.Fatalf("default accent must pass: %v", c)
	}
}

func TestIntegrationBranding(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-br1")
	seed(t, s, "itest-br2")
	t.Cleanup(func() {
		s.st.Pool.Exec(t.Context(), `DELETE FROM tenant_branding WHERE tenant_id IN ('itest-br1','itest-br2')`)
	})
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/branding", s.getBranding)
	api.HandleFunc("PUT /v1/branding", s.putBranding)
	put := func(tenant, role, body string) int { return call(api, tenant, role, "PUT", "/v1/branding", body).Code }
	if c := put("itest-br1", "admin", `{"product_name":"Acme Plant IoT","accent":"#1a56db"}`); c != 200 {
		t.Fatalf("valid %d", c)
	}
	for _, bad := range []string{`{"product_name":"<script>"}`, `{"accent":"red"}`, `{"accent":"#ffee00"}`, `{"product_name":"` + string(make([]byte, 41)) + `"}`} {
		if c := put("itest-br1", "admin", bad); c != 400 {
			t.Fatalf("%q -> %d", bad, c)
		}
	}
	if put("itest-br1", "operator", `{"product_name":"x"}`) != 403 {
		t.Fatal("operator changed branding")
	}
	var out map[string]any
	json.Unmarshal(call(api, "itest-br1", "viewer", "GET", "/v1/branding", "").Body.Bytes(), &out)
	if out["product_name"] != "Acme Plant IoT" || out["accent"] != "#1a56db" {
		t.Fatalf("got %v", out)
	}
	json.Unmarshal(call(api, "itest-br2", "viewer", "GET", "/v1/branding", "").Body.Bytes(), &out)
	if out["product_name"] != "" {
		t.Fatalf("branding leaked across tenants: %v", out)
	}
	if put("itest-br1", "admin", `{"product_name":"","accent":""}`) != 200 {
		t.Fatal("reset")
	}
}
