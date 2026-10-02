package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestIntegrationDirectGatewayKind(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-dd1")
	clean := func() {
		s.st.Pool.Exec(context.Background(), `DELETE FROM enrollment_tokens WHERE tenant_id='itest-dd1'`)
		s.st.Pool.Exec(context.Background(), `DELETE FROM gateways WHERE tenant_id='itest-dd1' AND serial LIKE 'MCU-%'`)
	}
	clean()
	t.Cleanup(clean)
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/enrollment/tokens", s.mintEnrollmentToken)
	mint := func(serial, kind string) int {
		return call(api, "itest-dd1", "admin", "POST", "/v1/enrollment/tokens",
			`{"site_id":"itest-dd1-site","serial":"`+serial+`"`+kind+`}`).Code
	}
	w := call(api, "itest-dd1", "admin", "POST", "/v1/enrollment/tokens", `{"site_id":"itest-dd1-site","serial":"MCU-A","kind":"direct"}`)
	if w.Code != 201 {
		t.Fatalf("direct: %d", w.Code)
	}
	var minted struct {
		ClaimCode    string `json:"claim_code"`
		EnrollString string `json:"enroll_string"`
	}
	json.Unmarshal(w.Body.Bytes(), &minted)
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(minted.EnrollString, "hexmon-enroll:1:"))
	var parts map[string]string
	if err != nil || json.Unmarshal(raw, &parts) != nil || parts["code"] != minted.ClaimCode || parts["serial"] != "MCU-A" || !strings.HasPrefix(parts["api"], "http") {
		t.Fatalf("enroll string: %q %v", minted.EnrollString, err)
	}
	if c := mint("MCU-B", ``); c != 201 {
		t.Fatalf("default: %d", c)
	}
	if c := mint("MCU-C", `,"kind":"root"`); c != 400 {
		t.Fatalf("bad kind: %d", c)
	}
	var a, b string
	s.st.Pool.QueryRow(t.Context(), `SELECT kind FROM gateways WHERE serial='MCU-A'`).Scan(&a)
	s.st.Pool.QueryRow(t.Context(), `SELECT kind FROM gateways WHERE serial='MCU-B'`).Scan(&b)
	if a != "direct" || b != "edge" {
		t.Fatalf("kinds %q %q", a, b)
	}
}
