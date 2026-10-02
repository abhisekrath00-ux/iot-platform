package main

import (
	"net/http"
	"testing"
)

func TestIntegrationWebhookChannelValidation(t *testing.T) {
	s, _ := testServer(t)
	seed(t, s, "itest-wh1")
	t.Cleanup(func() { s.st.Pool.Exec(t.Context(), `DELETE FROM notification_channels WHERE tenant_id='itest-wh1'`) })
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/notifications/channels", s.createChannel)
	post := func(role, body string) int {
		return call(api, "itest-wh1", role, "POST", "/v1/notifications/channels", body).Code
	}
	if c := post("admin", `{"type":"webhook","target":"https://hooks.example.com/iot"}`); c != 201 {
		t.Fatalf("valid webhook: %d", c)
	}
	for _, bad := range []string{`{"type":"webhook","target":"file:///etc/passwd"}`, `{"type":"webhook","target":"http://u:p@h/"}`, `{"type":"webhook","target":"not a url"}`} {
		if c := post("admin", bad); c != 400 {
			t.Fatalf("%s -> %d", bad, c)
		}
	}
	for _, ok := range []string{`{"type":"kafka","target":"kafka://k1:9092/alerts"}`, `{"type":"amqp","target":"amqp://mq:5672/v?exchange=iot&key=a"}`} {
		if c := post("admin", ok); c != 201 {
			t.Fatalf("%s -> %d", ok, c)
		}
	}
	for _, bad := range []string{`{"type":"kafka","target":"kafka://k/alerts"}`, `{"type":"amqp","target":"amqp://u:p@mq/v?key=a"}`, `{"type":"mqtt","target":"x"}`} {
		if c := post("admin", bad); c != 400 {
			t.Fatalf("%s -> %d", bad, c)
		}
	}
	if c := post("operator", `{"type":"webhook","target":"https://hooks.example.com/iot"}`); c != 403 {
		t.Fatalf("operator created a channel: %d", c)
	}
}
