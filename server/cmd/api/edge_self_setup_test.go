package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

func TestIntegrationEdgeSelfSetupRequiresOwnActiveGatewayAndSecureBroker(t *testing.T) {
	s, _ := testServer(t)
	tenant := "itest-self-setup"
	seed(t, s, tenant)
	handler := http.HandlerFunc(s.edgeSelfSetup)
	run := func(role, principal, owner string, key bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/edge/self/setup", nil)
		c := context.WithValue(r.Context(), auth.CtxTenant, owner)
		c = context.WithValue(c, auth.CtxRole, role)
		c = context.WithValue(c, auth.CtxUser, principal)
		if key {
			c = context.WithValue(c, auth.CtxViaKey, true)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r.WithContext(c))
		return w
	}
	t.Setenv("EDGE_MQTT_HOST", "mqtt.example.invalid")
	t.Setenv("EDGE_MQTT_PORT", "8883")
	t.Setenv("EDGE_MQTT_TLS", "true")
	t.Setenv("FLEET_SIGNING_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	w := run("gateway", tenant+"-gw", tenant, false)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var out struct {
		GatewayID string `json:"gateway_id"`
		YAML      string `json:"config_yaml"`
		Signed    bool   `json:"signed_source_updates_available"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.GatewayID != tenant+"-gw" || !out.Signed || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(out, w.Header())
	}
	for _, v := range []string{"enabled: false", "remote_restart: false", "managed_devices: false", "outputs: []", "rules: []", "allowed_commands: []", "tls: true", `host: "mqtt.example.invalid"`} {
		if !strings.Contains(out.YAML, v) {
			t.Fatal("missing", v, out.YAML)
		}
	}
	for _, role := range []string{"admin", "operator", "viewer"} {
		if got := run(role, tenant+"-gw", tenant, false).Code; got != 403 {
			t.Fatal(role, got)
		}
	}
	if got := run("gateway", tenant+"-gw", tenant, true).Code; got != 403 {
		t.Fatal("key", got)
	}
	if got := run("gateway", tenant+"-gw", "foreign", false).Code; got != 404 {
		t.Fatal("foreign", got)
	}
	for _, host := range []string{"", "localhost", "127.0.0.1", "0.0.0.0", "host/path", "user@host"} {
		t.Setenv("EDGE_MQTT_HOST", host)
		if got := run("gateway", tenant+"-gw", tenant, false).Code; got != 409 {
			t.Fatal(host, got)
		}
	}
	t.Setenv("EDGE_MQTT_HOST", "mqtt.example.invalid")
	t.Setenv("EDGE_MQTT_TLS", "false")
	if got := run("gateway", tenant+"-gw", tenant, false).Code; got != 409 {
		t.Fatal("plaintext", got)
	}
	t.Setenv("EDGE_MQTT_TLS", "true")
	t.Setenv("FLEET_SIGNING_KEY", "")
	if got := run("gateway", tenant+"-gw", tenant, false).Code; got != 200 {
		t.Fatal("no key should allow connection-only", got)
	}
	_, err := s.st.Pool.Exec(t.Context(), `UPDATE gateways SET status='revoked' WHERE id=$1`, tenant+"-gw")
	if err != nil {
		t.Fatal(err)
	}
	if got := run("gateway", tenant+"-gw", tenant, false).Code; got != 403 {
		t.Fatal("revoked", got)
	}
}
