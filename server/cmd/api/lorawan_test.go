package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIntegrationLoRaWANUplink(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-lw1")
	pool := s.st.Pool
	clean := func() {
		pool.Exec(context.Background(), `DELETE FROM device_tokens WHERE tenant_id='itest-lw1'`)
		pool.Exec(context.Background(), `DELETE FROM telemetry WHERE tenant_id='itest-lw1'`)
	}
	clean()
	t.Cleanup(clean)
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/devices/{id}/tokens", s.createDeviceToken)
	api.HandleFunc("POST /v1/lorawan/uplink", s.lorawanUplink)
	w := call(api, "itest-lw1", "admin", "POST", "/v1/devices/itest-lw1-dev/tokens", `{"name":"lora","expires_in_days":30}`)
	var tok struct{ Token string }
	json.Unmarshal(w.Body.Bytes(), &tok)
	post := func(token, body string) (int, string) {
		r := httptest.NewRequest("POST", "/v1/lorawan/uplink", strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, r)
		return rec.Code, rec.Body.String()
	}
	// ChirpStack v4 shape
	c, b := post(tok.Token, `{"time":"`+nowRFC()+`","deviceInfo":{"devEui":"a1"},"object":{"temp":21.5,"battery":{"v":3.1},"ok":true,"label":"x"}}`)
	if c != 200 || !strings.Contains(b, `"accepted":1`) { // only temp is a registered point
		t.Fatalf("chirpstack: %d %s", c, b)
	}
	// The Things Stack shape
	c, b = post(tok.Token, `{"received_at":"`+nowRFC()+`","uplink_message":{"decoded_payload":{"temp":22.5}}}`)
	if c != 200 || !strings.Contains(b, `"accepted":1`) {
		t.Fatalf("tts: %d %s", c, b)
	}
	// Actility ThingPark shape: numbers arrive as strings, raw hex is ignored
	c, b = post(tok.Token, `{"DevEUI_uplink":{"Time":"`+nowRFC()+`","DevEUI":"0004A30B001A2B3C","payload_hex":"0102","temp":"23.5"}}`)
	if c != 200 || !strings.Contains(b, `"accepted":1`) {
		t.Fatalf("thingpark: %d %s", c, b)
	}
	// Sigfox callback: epoch time, numeric strings, hex data ignored
	c, b = post(tok.Token, fmt.Sprintf(`{"device":"1A2B3C","time":"%d","data":"0102030405060708090a0b0c","seqNumber":"7","temp":"24.5"}`, time.Now().Unix()))
	if c != 200 || !strings.Contains(b, `"accepted":1`) {
		t.Fatalf("sigfox: %d %s", c, b)
	}
	var n int
	pool.QueryRow(t.Context(), `SELECT count(*) FROM telemetry WHERE tenant_id='itest-lw1' AND point_id='temp'`).Scan(&n)
	if n != 4 {
		t.Fatalf("stored %d", n)
	}
	for name, tc := range map[string][2]string{
		"no token":     {"", `{"object":{"temp":1}}`},
		"bad token":    {"hxd_x.y", `{"object":{"temp":1}}`},
		"no decoded":   {tok.Token, `{"data":"AQID"}`},
		"only strings": {tok.Token, `{"object":{"s":"a"}}`},
		"bad json":     {tok.Token, `{`},
	} {
		c, _ := post(tc[0], tc[1])
		if c != 401 && c != 422 && c != 400 {
			t.Errorf("%s: %d", name, c)
		}
		if c == 200 {
			t.Errorf("%s accepted", name)
		}
	}
}

func nowRFC() string { return time.Now().UTC().Format(time.RFC3339) }
