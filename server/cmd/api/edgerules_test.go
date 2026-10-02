package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestIntegrationEdgeRules(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-er1")
	t.Cleanup(func() { s.st.Pool.Exec(t.Context(), `DELETE FROM gateway_edge_rules WHERE tenant_id='itest-er1'`) })
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/gateways", s.listGateways)
	api.HandleFunc("GET /v1/gateways/{id}/edge-rules", s.getEdgeRules)
	api.HandleFunc("PUT /v1/gateways/{id}/edge-rules", s.putEdgeRules)
	api.HandleFunc("GET /v1/gateways/{id}/edge-config", s.gatewayEdgeConfig)
	put := func(role, body string) int {
		return call(api, "itest-er1", role, "PUT", "/v1/gateways/itest-er1-gw/edge-rules", body).Code
	}
	good := `{"outputs":[{"name":"siren","kind":"gpio_file","path":"/sys/class/gpio/gpio17/value","max_on_seconds":300}],
	 "rules":[{"id":"offline","type":"link_down","for_seconds":30,"output":"siren"},
	          {"id":"hot","name":"Boiler hot","type":"threshold","device":"itest-er1-dev","point":"temp","op":">","value":90,"output":"siren","pattern":"pulse"}]}`
	if c := put("admin", good); c != 200 {
		t.Fatalf("good: %d", c)
	}
	if c := put("operator", good); c != 403 {
		t.Fatalf("operator wrote rules: %d", c)
	}
	for name, bad := range map[string]string{
		"path traversal":  `{"outputs":[{"name":"s","kind":"gpio_file","path":"/sys/class/gpio/../../etc/passwd"}],"rules":[]}`,
		"outside paths":   `{"outputs":[{"name":"s","kind":"gpio_file","path":"/etc/passwd"}],"rules":[]}`,
		"exec kind":       `{"outputs":[{"name":"s","kind":"exec","path":"/bin/sh"}],"rules":[]}`,
		"unknown output":  `{"outputs":[],"rules":[{"id":"r","type":"link_down","output":"pump"}]}`,
		"foreign device":  `{"outputs":[{"name":"s","kind":"simulate"}],"rules":[{"id":"r","type":"stale","device":"other-tenant-dev","for_seconds":30,"output":"s"}]}`,
		"unknown field":   `{"outputs":[{"name":"s","kind":"simulate","class":"process"}],"rules":[]}`,
		"newline in name": `{"outputs":[{"name":"s","kind":"simulate"}],"rules":[{"id":"r","name":"a\nb: c","type":"link_down","output":"s"}]}`,
		"short stale":     `{"outputs":[{"name":"s","kind":"simulate"}],"rules":[{"id":"r","type":"stale","device":"itest-er1-dev","for_seconds":1,"output":"s"}]}`,
	} {
		if c := put("admin", bad); c != 400 {
			t.Errorf("%s accepted: %d", name, c)
		}
	}
	w := call(api, "itest-er1", "admin", "GET", "/v1/gateways/itest-er1-gw/edge-config", "")
	y := w.Body.String()
	for _, want := range []string{"outputs:", "class: alarm", "kind: gpio_file", "max_on: 300s", "rules:", "type: link_down", "for: 30s", "op: \"\\u003e\"", "pattern: pulse"} {
		if !strings.Contains(y, want) {
			t.Errorf("edge-config missing %q:\n%s", want, y)
		}
	}
	if c := call(api, "itest-er1", "operator", "GET", "/v1/gateways/itest-er1-gw/edge-rules", "").Code; c != 200 {
		t.Errorf("operator read: %d", c)
	}
	if w := call(api, "itest-er1", "admin", "GET", "/v1/gateways", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "itest-er1-gw") || strings.Contains(w.Body.String(), "itest-dd1") {
		t.Errorf("gateway list: %d %s", w.Code, w.Body.String())
	}
	if c := call(api, "itest-other", "admin", "GET", "/v1/gateways/itest-er1-gw/edge-rules", "").Code; c != 404 {
		t.Errorf("cross-tenant read: %d", c)
	}
}
