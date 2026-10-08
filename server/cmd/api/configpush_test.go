package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/fleet"
)

func TestIntegrationConfigPush(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-cp1")
	pool := s.st.Pool
	clean := func() {
		pool.Exec(t.Context(), `DELETE FROM gateway_config_pushes WHERE tenant_id='itest-cp1'`)
		pool.Exec(t.Context(), `DELETE FROM audit_log WHERE tenant_id='itest-cp1'`)
	}
	clean()
	t.Cleanup(clean)
	for _, u := range [][2]string{{"cp-a", "admin"}, {"cp-v", "viewer"}} {
		pool.Exec(t.Context(), `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES($1,'itest-cp1',$1||'@cp-test.example','x',$2) ON CONFLICT DO NOTHING`, u[0], u[1])
	}
	t.Cleanup(func() { pool.Exec(t.Context(), `DELETE FROM users WHERE id IN ('cp-a','cp-v')`) })
	var sent []map[string]any
	var topics []string
	s.opsPublishHook = func(topic string, b []byte) error {
		m := map[string]any{}
		json.Unmarshal(b, &m)
		sent, topics = append(sent, m), append(topics, topic)
		return nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/gateways/{id}/config-push", s.pushGatewayConfig)
	mux.HandleFunc("GET /v1/gateways/{id}/config-push", s.listConfigPushes)
	h := s.activeUser(mux)
	do := func(user, role, method, path string) (int, string) {
		w := callAs(h, "itest-cp1", user, role, method, path, ``)
		return w.Code, w.Body.String()
	}
	p := "/v1/gateways/itest-cp1-gw/config-push"

	// no signing key: refused, nothing sent
	t.Setenv("FLEET_SIGNING_KEY", "")
	if c, _ := do("cp-a", "admin", "POST", p); c != 409 || len(sent) != 0 {
		t.Fatalf("no key = %d", c)
	}
	seedKey := base64.StdEncoding.EncodeToString(make([]byte, 32))
	t.Setenv("FLEET_SIGNING_KEY", seedKey)
	if c, _ := do("cp-v", "viewer", "POST", p); c != 403 {
		t.Fatalf("viewer = %d", c)
	}
	if c, _ := do("cp-a", "admin", "POST", "/v1/gateways/nope/config-push"); c != 404 {
		t.Fatalf("unknown gateway = %d", c)
	}
	c, body := do("cp-a", "admin", "POST", p)
	if c != 202 || len(sent) != 1 || topics[0] != "t/itest-cp1/g/itest-cp1-gw/config" {
		t.Fatalf("push: %d %s %v", c, body, topics)
	}
	m := sent[0]
	yml, _ := m["devices_yaml"].(string)
	if !strings.HasPrefix(yml, "devices:") || strings.Contains(yml, "mqtt") || strings.Contains(yml, "tenant_id") {
		t.Fatalf("only the device list may travel: %q", yml)
	}
	sum := sha256.Sum256([]byte(yml))
	if m["sha256"] != hex.EncodeToString(sum[:]) || m["version"] != 1.0 {
		t.Fatalf("envelope: %+v", m)
	}
	// the signature verifies against the public half of the key, over the canonical bytes
	pub := ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey)
	exp, _ := time.Parse(time.RFC3339, m["expires_at"].(string))
	sig, _ := base64.StdEncoding.DecodeString(m["signature"].(string))
	if !ed25519.Verify(pub, fleet.CanonicalConfigPush("itest-cp1", m["gateway_serial"].(string), 1, m["push_id"].(string), m["sha256"].(string), exp), sig) {
		t.Fatal("signature does not verify")
	}
	// one at a time
	if c, _ := do("cp-a", "admin", "POST", p); c != 409 {
		t.Fatalf("second open push = %d", c)
	}
	// the gateway's answers (what ingest stores) walk it forward; a new version follows
	pool.Exec(t.Context(), `UPDATE gateway_config_pushes SET status='confirmed' WHERE tenant_id='itest-cp1'`)
	if c, _ := do("cp-a", "admin", "POST", p); c != 202 || sent[len(sent)-1]["version"] != 2.0 {
		t.Fatalf("second push after confirm = %d", c)
	}
	c, body = do("cp-a", "admin", "GET", p)
	if c != 200 || !strings.Contains(body, `"version":2`) || strings.Contains(body, "devices_yaml") {
		t.Fatalf("list: %d %s", c, body)
	}
}

// Fixed vector: the same bytes are signed in edge/internal/cfgpush (TestCanonicalVector).
func TestConfigPushCanonicalVector(t *testing.T) {
	got := string(fleet.CanonicalConfigPush("ten", "SER1", 3, "push-0001", "abc", time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)))
	want := "hexmon-config-push-v1\nten\nSER1\n3\npush-0001\nabc\n2026-10-08T12:00:00Z"
	if got != want {
		t.Fatalf("%q", got)
	}
}
