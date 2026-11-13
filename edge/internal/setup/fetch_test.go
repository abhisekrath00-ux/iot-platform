package setup

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/claim"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"gopkg.in/yaml.v3"
)

func result() Result {
	return Result{GatewayID: "g", TenantID: "t", Serial: "s", YAML: "gateway_id: g\ntenant_id: t\nserial: s\nmqtt: {host: mqtt.example.invalid,port: 8883,tls: true}\nautodetect: {enabled: false}\nmanaged_devices: false\ndevices: []\n"}
}
func TestFetchBindsIdentityAndSafeDefaults(t *testing.T) {
	out := result()
	seen := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		if r.URL.Path != "/v1/edge/self/setup" || r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("bad request %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()
	id := &claim.Identity{GatewayID: "g", IngestToken: "token"}
	b, err := Fetch(context.Background(), srv.Client(), srv.URL, id, "s", false)
	if err != nil {
		t.Fatal(err)
	}
	var c config.Config
	if yaml.Unmarshal(b, &c) != nil || c.ManagedDevices || c.Autodetect.Enabled == nil || *c.Autodetect.Enabled {
		t.Fatal("unsafe config")
	}
	for _, tc := range []struct {
		name string
		mut  func(*Result)
	}{
		{"wrong gateway", func(r *Result) { r.GatewayID = "foreign" }}, {"wrong serial", func(r *Result) { r.Serial = "other" }}, {"wrong config identity", func(r *Result) { r.YAML = strings.Replace(r.YAML, "gateway_id: g", "gateway_id: other", 1) }},
		{"plaintext", func(r *Result) { r.YAML = strings.Replace(r.YAML, "tls: true", "tls: false", 1) }}, {"loopback broker", func(r *Result) { r.YAML = strings.Replace(r.YAML, "mqtt.example.invalid", "127.0.0.1", 1) }},
		{"discovery", func(r *Result) { r.YAML = strings.Replace(r.YAML, "enabled: false", "enabled: true", 1) }}, {"implicit discovery", func(r *Result) { r.YAML = strings.Replace(r.YAML, "autodetect: {enabled: false}\n", "", 1) }},
		{"managed without permission", func(r *Result) {
			r.YAML = strings.Replace(r.YAML, "managed_devices: false", "managed_devices: true", 1)
		}},
		{"commands", func(r *Result) { r.YAML += "allowed_commands: [restart]\n" }}, {"local file", func(r *Result) { r.YAML += "queue_path: /tmp/replaced\n" }}, {"status exposure", func(r *Result) { r.YAML += "ui: {listen: '0.0.0.0:8088'}\n" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out = result()
			tc.mut(&out)
			if _, err := Fetch(context.Background(), srv.Client(), srv.URL, id, "s", false); err == nil {
				t.Fatal("accepted unsafe server response")
			}
		})
	}
	out = result()
	if _, err := Fetch(context.Background(), srv.Client(), srv.URL, id, "s", true); err == nil {
		t.Fatal("managed without signing")
	}
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	out.Signed = true
	out.YAML += "fleet_public_key: " + base64.StdEncoding.EncodeToString(pub) + "\n"
	b, err = Fetch(context.Background(), srv.Client(), srv.URL, id, "s", true)
	if err != nil {
		t.Fatal(err)
	}
	yaml.Unmarshal(b, &c)
	if !c.ManagedDevices {
		t.Fatal("explicit optin not set")
	}
	if seen < 3 {
		t.Fatal("no live fetch")
	}
}
func TestFetchRejectsURLsBeforeSendingAndNeverFollowsRedirect(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Redirect(w, r, "http://localhost/other", 302)
	}))
	defer srv.Close()
	id := &claim.Identity{GatewayID: "g", IngestToken: "secret"}
	cl := &http.Client{Timeout: time.Second}
	for _, u := range []string{"http://remote.example", "ftp://localhost", "https://name:pass@example.invalid", "https://example.invalid/path", "https://example.invalid?x=1", "https://example.invalid#x"} {
		if _, err := Fetch(context.Background(), cl, u, id, "s", false); err == nil {
			t.Fatal("accepted", u)
		}
	}
	if calls != 0 {
		t.Fatal("request sent for bad URL")
	}
	if _, err := Fetch(context.Background(), cl, srv.URL, id, "s", false); err == nil {
		t.Fatal("redirect accepted")
	}
	if calls != 1 {
		t.Fatal("followed redirect", calls)
	}
}
