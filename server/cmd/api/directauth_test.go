package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/brokeracl"
)

func TestIntegrationDirectPasswordAuth(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-dp1")
	ctx := context.Background()
	clean := func() {
		s.st.Pool.Exec(ctx, `DELETE FROM gateways WHERE tenant_id='itest-dp1' AND serial LIKE 'PW-%'`)
		s.st.Pool.Exec(ctx, `DELETE FROM tenant_direct_auth WHERE tenant_id='itest-dp1'`)
	}
	clean()
	t.Cleanup(clean)
	dir := t.TempDir()
	t.Setenv("BROKER_ACL_FILE", filepath.Join(dir, "acl"))
	t.Setenv("BROKER_DIRECT_PASSWD_FILE", filepath.Join(dir, "passwd"))
	api := http.NewServeMux()
	api.HandleFunc("PUT /v1/direct-auth/policy", s.putDirectAuthPolicy)
	api.HandleFunc("GET /v1/direct-auth/policy", s.getDirectAuthPolicy)
	api.HandleFunc("POST /v1/direct-devices/password", s.createPasswordDevice)
	api.HandleFunc("POST /v1/direct-devices/{id}/rotate", s.rotatePasswordDevice)
	api.HandleFunc("POST /v1/direct-devices/{id}/revoke", s.revokeDirectDevice)
	api.HandleFunc("GET /v1/direct-devices", s.listDirectDevices)
	do := func(role, method, path, body string) (int, string) {
		w := call(api, "itest-dp1", role, method, path, body)
		return w.Code, w.Body.String()
	}
	mk := `{"site_id":"itest-dp1-site","serial":"PW-1"}`
	// off by default
	if c, _ := do("admin", "POST", "/v1/direct-devices/password", mk); c != 403 {
		t.Fatalf("created while disabled: %d", c)
	}
	if c, b := do("admin", "GET", "/v1/direct-auth/policy", ""); c != 200 || !strings.Contains(b, `"password_enabled":false`) || !strings.Contains(b, "weaker") {
		t.Fatalf("policy: %d %s", c, b)
	}
	if c, _ := do("operator", "PUT", "/v1/direct-auth/policy", `{"password_enabled":true}`); c != 403 {
		t.Fatalf("operator changed policy: %d", c)
	}
	if c, _ := do("admin", "PUT", "/v1/direct-auth/policy", `{"password_enabled":true}`); c != 200 {
		t.Fatal(c)
	}
	if c, _ := do("operator", "POST", "/v1/direct-devices/password", mk); c != 403 {
		t.Fatalf("operator created: %d", c)
	}
	for _, bad := range []string{`{"site_id":"itest-dp1-site","serial":"ingest"}`, `{"site_id":"itest-dp1-site","serial":"a b"}`, `{"site_id":"nope","serial":"PW-9"}`} {
		if c, _ := do("admin", "POST", "/v1/direct-devices/password", bad); c == 201 {
			t.Fatalf("accepted %s", bad)
		}
	}
	c, b := do("admin", "POST", "/v1/direct-devices/password", mk)
	if c != 201 {
		t.Fatalf("%d %s", c, b)
	}
	var out struct{ GatewayID, Username, Password, PublishTopic string }
	json.Unmarshal([]byte(strings.NewReplacer("gateway_id", "GatewayID", "username", "Username", "password", "Password", "publish_topic", "PublishTopic").Replace(b)), &out)
	if out.Password == "" || out.Username != "PW-1" || !strings.HasSuffix(out.PublishTopic, "/"+out.GatewayID+"/telemetry") {
		t.Fatalf("%s", b)
	}
	// only a hash is stored
	var hash string
	s.st.Pool.QueryRow(ctx, `SELECT broker_pw_hash FROM gateways WHERE id=$1`, out.GatewayID).Scan(&hash)
	if strings.Contains(hash, out.Password) || !brokeracl.VerifyPassword(out.Password, hash) {
		t.Fatalf("stored hash wrong: %s", hash)
	}
	if c, _ := do("admin", "POST", "/v1/direct-devices/password", mk); c != 409 {
		t.Fatalf("duplicate serial: %d", c)
	}
	pw, _ := os.ReadFile(filepath.Join(dir, "passwd"))
	acl, _ := os.ReadFile(filepath.Join(dir, "acl"))
	if !strings.Contains(string(pw), "PW-1:$7$101$") || strings.Contains(string(pw), out.Password) {
		t.Fatalf("passwd file: %s", pw)
	}
	blk := string(acl)[strings.Index(string(acl), "user PW-1"):]
	if !strings.Contains(blk, "/"+out.GatewayID+"/telemetry") || strings.Contains(blk, "topic read") || strings.Contains(blk, "cmd") {
		t.Fatalf("acl block: %s", blk)
	}
	// rotate: old secret stops verifying
	c, b = do("admin", "POST", "/v1/direct-devices/"+out.GatewayID+"/rotate", "")
	if c != 200 || strings.Contains(b, out.Password) {
		t.Fatalf("rotate: %d %s", c, b)
	}
	var h2 string
	s.st.Pool.QueryRow(ctx, `SELECT broker_pw_hash FROM gateways WHERE id=$1`, out.GatewayID).Scan(&h2)
	if h2 == hash || brokeracl.VerifyPassword(out.Password, h2) {
		t.Fatal("rotate did not replace the secret")
	}
	// disabling the policy removes the device from the broker files
	do("admin", "PUT", "/v1/direct-auth/policy", `{"password_enabled":false}`)
	pw, _ = os.ReadFile(filepath.Join(dir, "passwd"))
	acl, _ = os.ReadFile(filepath.Join(dir, "acl"))
	if strings.Contains(string(pw), "PW-1") || strings.Contains(string(acl), "user PW-1") {
		t.Fatal("device stayed on the broker after the policy was disabled")
	}
	do("admin", "PUT", "/v1/direct-auth/policy", `{"password_enabled":true}`)
	if c, _ := do("admin", "POST", "/v1/direct-devices/"+out.GatewayID+"/revoke", ""); c != 200 {
		t.Fatalf("revoke %d", c)
	}
	pw, _ = os.ReadFile(filepath.Join(dir, "passwd"))
	if strings.Contains(string(pw), "PW-1") {
		t.Fatal("revoked device still in passwd")
	}
	if c, b := do("admin", "GET", "/v1/direct-devices", ""); c != 200 || strings.Contains(b, "hash") || !strings.Contains(b, `"revoked"`) {
		t.Fatalf("list: %d %s", c, b)
	}
}
