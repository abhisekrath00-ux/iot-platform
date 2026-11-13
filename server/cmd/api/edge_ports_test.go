package main

import (
	"context"
	"crypto/tls"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIntegrationSerialInventoryIsExplicitOwnGatewayFreshAndNotProbe(t *testing.T) {
	s, _ := testServer(t)
	tenant := "itest-port-inventory"
	seed(t, s, tenant)
	m := http.NewServeMux()
	m.HandleFunc("POST /v1/edge/self/serial-ports", s.reportSerialPorts)
	m.HandleFunc("GET /v1/gateways/{id}/serial-ports", s.getSerialPorts)
	run := func(owner, principal, role, method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.TLS = &tls.ConnectionState{}
		c := context.WithValue(r.Context(), auth.CtxTenant, owner)
		c = context.WithValue(c, auth.CtxUser, principal)
		c = context.WithValue(c, auth.CtxRole, role)
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r.WithContext(c))
		return w
	}
	path := "/v1/edge/self/serial-ports"
	if w := run(tenant, tenant+"-gw", "gateway", "POST", path, `{"ports":["COM3","COM3","COM12"]}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	get := "/v1/gateways/" + tenant + "-gw/serial-ports"
	w := run(tenant, "test-user", "admin", "GET", get, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"fresh":true`) || !strings.Contains(w.Body.String(), `["COM3","COM12"]`) {
		t.Fatal(w.Code, w.Body)
	}
	if w := run("foreign", "test-user", "admin", "GET", get, ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := run(tenant, tenant+"-gw", "gateway", "POST", path, `{"ports":["bad\nport"]}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	s.st.Pool.Exec(context.Background(), `UPDATE edge_serial_port_inventory SET reported_at=now()-interval '6 minutes' WHERE gateway_id=$1`, tenant+"-gw")
	w = run(tenant, "test-user", "admin", "GET", get, "")
	if !strings.Contains(w.Body.String(), `"fresh":false`) || !strings.Contains(w.Body.String(), `"ports":[]`) {
		t.Fatal(w.Body)
	}
}
