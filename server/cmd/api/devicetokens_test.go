package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIntegrationDeviceTokens(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-dt1")
	seed(t, s, "itest-dt2")
	pool := s.st.Pool
	clean := func() {
		pool.Exec(context.Background(), `DELETE FROM device_tokens WHERE tenant_id IN ('itest-dt1','itest-dt2')`)
	}
	clean()
	t.Cleanup(clean)
	pool.Exec(context.Background(), `INSERT INTO devices(id,tenant_id,gateway_id,profile,name) SELECT 'itest-dt1-other',tenant_id,gateway_id,profile,'other' FROM devices WHERE id='itest-dt1-dev' ON CONFLICT DO NOTHING`)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM devices WHERE id='itest-dt1-other'`) })

	api := http.NewServeMux()
	api.HandleFunc("POST /v1/devices/{id}/tokens", s.createDeviceToken)
	api.HandleFunc("GET /v1/devices/{id}/tokens", s.listDeviceTokens)
	api.HandleFunc("DELETE /v1/devices/{id}/tokens/{tid}", s.revokeDeviceToken)
	api.HandleFunc("POST /v1/device/ingest", s.deviceIngest)

	// operators cannot mint; other tenants cannot see the device
	if w := call(api, "itest-dt1", "operator", "POST", "/v1/devices/itest-dt1-dev/tokens", `{"name":"x","expires_in_days":30}`); w.Code != 403 {
		t.Fatalf("operator minted: %d", w.Code)
	}
	if w := call(api, "itest-dt2", "admin", "POST", "/v1/devices/itest-dt1-dev/tokens", `{"name":"x","expires_in_days":30}`); w.Code != 404 {
		t.Fatalf("cross-tenant mint: %d", w.Code)
	}
	if w := call(api, "itest-dt1", "admin", "POST", "/v1/devices/itest-dt1-dev/tokens", `{"name":"x","expires_in_days":99999}`); w.Code != 400 {
		t.Fatalf("overlong expiry: %d", w.Code)
	}
	w := call(api, "itest-dt1", "admin", "POST", "/v1/devices/itest-dt1-dev/tokens", `{"name":"esp32","expires_in_days":30}`)
	if w.Code != 201 {
		t.Fatalf("mint: %d %s", w.Code, w.Body)
	}
	var tok struct{ ID, Token string }
	json.Unmarshal(w.Body.Bytes(), &tok)
	var stored int
	pool.QueryRow(t.Context(), `SELECT count(*) FROM device_tokens WHERE secret_hash = convert_to($1,'UTF8')`, tok.Token).Scan(&stored)
	if stored != 0 || !strings.HasPrefix(tok.Token, "hxd_") {
		t.Fatal("token stored in the clear or wrong format")
	}
	ingest := func(token, body string) (int, string) {
		r := httptest.NewRequest("POST", "/v1/device/ingest", strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, r)
		return rec.Code, rec.Body.String()
	}
	good := `{"readings":[{"point":"temp","value":21.5,"event_id":"dt-1"}]}`
	if c, b := ingest(tok.Token, good); c != 200 || !strings.Contains(b, `"accepted":1`) {
		t.Fatalf("ingest: %d %s", c, b)
	}
	// a body naming another device is ignored: the token fixes the device
	if c, _ := ingest(tok.Token, `{"device_id":"itest-dt1-other","readings":[{"point":"temp","value":22,"event_id":"dt-2"}]}`); c != 200 {
		t.Fatalf("second ingest %d", c)
	}
	var other int
	pool.QueryRow(t.Context(), `SELECT count(*) FROM telemetry WHERE event_id='dt-2' AND device_id<>'itest-dt1-dev'`).Scan(&other)
	if other != 0 {
		t.Fatal("token wrote to another device")
	}
	for name, bad := range map[string]string{"none": "", "garbage": "hxd_x.y", "wrong secret": "hxd_" + tok.ID + ".nope", "api key prefix": "hxk_" + tok.ID + ".x"} {
		if c, _ := ingest(bad, good); c != 401 {
			t.Fatalf("%s accepted: %d", name, c)
		}
	}
	// revoke
	if w := call(api, "itest-dt1", "admin", "DELETE", "/v1/devices/itest-dt1-dev/tokens/"+tok.ID, ""); w.Code != 204 {
		t.Fatalf("revoke %d", w.Code)
	}
	if c, _ := ingest(tok.Token, good); c != 401 {
		t.Fatalf("revoked token accepted: %d", c)
	}
	// expiry
	w = call(api, "itest-dt1", "admin", "POST", "/v1/devices/itest-dt1-dev/tokens", `{"name":"short","expires_in_days":1}`)
	var t2 struct{ ID, Token string }
	json.Unmarshal(w.Body.Bytes(), &t2)
	pool.Exec(t.Context(), `UPDATE device_tokens SET expires_at = now() - interval '1 second' WHERE id=$1`, t2.ID)
	if c, _ := ingest(t2.Token, good); c != 401 {
		t.Fatalf("expired token accepted: %d", c)
	}
	// list never shows secrets
	if w := call(api, "itest-dt1", "admin", "GET", "/v1/devices/itest-dt1-dev/tokens", ""); w.Code != 200 || strings.Contains(w.Body.String(), "hxd_") {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	pool.Exec(context.Background(), `DELETE FROM telemetry WHERE event_id IN ('dt-1','dt-2')`)
}
