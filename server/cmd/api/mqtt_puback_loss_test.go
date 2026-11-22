package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/mqttconfig"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/eclipse/paho.mqtt.golang/packets"
)

// Test-only fault boundary AFTER certificate verification/decryption. It consumes
// one genuine broker PUBACK without giving it to Paho, then drops that connection.
// It never changes a certificate requirement or exposes plaintext on a listener.
type lostBrokerPUBACKConn struct {
	net.Conn
	drop     *atomic.Bool
	lost     *atomic.Int32
	pending  []byte
	keepOpen bool
}

func (c *lostBrokerPUBACKConn) Read(b []byte) (int, error) {
	if len(c.pending) == 0 {
		packet, err := packets.ReadPacket(c.Conn)
		if err != nil {
			return 0, err
		}
		if _, ok := packet.(*packets.PubackPacket); ok && c.drop.CompareAndSwap(true, false) {
			c.lost.Add(1)
			if c.keepOpen {
				return c.Read(b)
			}
			c.Conn.Close()
			return 0, io.ErrUnexpectedEOF
		}
		var encoded bytes.Buffer
		if err := packet.Write(&encoded); err != nil {
			return 0, err
		}
		c.pending = encoded.Bytes()
	}
	n := copy(b, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

// Characterization of an OPEN safety gap, not idempotency/delivery acceptance.
// No executor or device is subscribed, just a synthetic read-only observer.
func verifyActualPUBACKLossReconnect(t *testing.T, port, ca, gc, gk, ac, ak string, reconnect bool) {
	t.Helper()
	options := func(id, cert, key string) *mqtt.ClientOptions {
		o, e := mqttconfig.Options(func(k string) string {
			return map[string]string{"MQTT_TLS": "true", "MQTT_HOST": "127.0.0.1", "MQTT_PORT": port, "MQTT_CA_FILE": ca, "MQTT_CERT_FILE": cert, "MQTT_KEY_FILE": key}[k]
		}, id)
		if e != nil {
			t.Fatal(e)
		}
		return o
	}
	observer := mqtt.NewClient(options("puback-loss-observer", gc, gk))
	token := observer.Connect()
	if !token.WaitTimeout(5*time.Second) || token.Error() != nil {
		t.Fatal("observer connect", token.Error())
	}
	defer observer.Disconnect(100)
	topic := "t/itest-mqpipe/g/itest-mqpipe-gw/config"
	payload := []byte(`{"synthetic_read_only_observer":true,"attempt":"one-publish-call"}`)
	var delivered atomic.Int32
	seen := make(chan struct{}, 8)
	token = observer.Subscribe(topic, 1, func(_ mqtt.Client, m mqtt.Message) {
		if m.Topic() == topic && bytes.Equal(m.Payload(), payload) {
			delivered.Add(1)
			select {
			case seen <- struct{}{}:
			default:
			}
		}
	})
	if !token.WaitTimeout(5*time.Second) || token.Error() != nil {
		t.Fatal("observer subscribe", token.Error())
	}
	var drop atomic.Bool
	var lost, connections atomic.Int32
	o := options("puback-loss-production-api", ac, ak)
	if !o.AutoReconnect {
		t.Fatal("production reconnect default unexpectedly changed")
	}
	if !reconnect {
		o.SetAutoReconnect(false)
	} // isolated candidate only, never changes product options
	// Gap case retains production AutoReconnect=true. Only the local connection
	// implementation adds a single dropped genuine PUBACK/connection-loss fault.
	o.SetCustomOpenConnectionFn(func(uri *url.URL, opts mqtt.ClientOptions) (net.Conn, error) {
		conf := opts.TLSConfig.Clone()
		if conf.ServerName == "" {
			conf.ServerName = uri.Hostname()
		}
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", uri.Host, conf)
		if err != nil {
			return nil, err
		}
		connections.Add(1)
		return &lostBrokerPUBACKConn{Conn: conn, drop: &drop, lost: &lost}, nil
	})
	publisher := mqtt.NewClient(o)
	token = publisher.Connect()
	if !token.WaitTimeout(5*time.Second) || token.Error() != nil {
		t.Fatal("publisher connect", token.Error())
	}
	defer publisher.Disconnect(100)
	previousClient, previousErr := mqttClient, mqttErr
	mqttOnce.Do(func() {})
	mqttClient, mqttErr = publisher, nil
	t.Cleanup(func() { mqttClient, mqttErr = previousClient, previousErr; mqttOnce = sync.Once{} })
	drop.Store(true)
	result := make(chan error, 1)
	// EXACTLY ONE invocation, production opsPublish -> publishMQTTRetained ->
	// real Paho QoS1 Publish/token.Wait. No opsPublishHook or synthetic return error.
	go func() { result <- (&server{}).opsPublish(topic, payload) }()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	expectedDeliveries := int32(2)
	if !reconnect {
		expectedDeliveries = 1
	}
	for delivered.Load() < expectedDeliveries {
		select {
		case <-seen:
		case <-deadline.C:
			t.Fatalf("reconnect observation incomplete: deliveries=%d dropped_real_puback=%d connections=%d", delivered.Load(), lost.Load(), connections.Load())
		}
	}
	select {
	case err := <-result:
		if (err == nil) != reconnect {
			t.Fatalf("unexpected completion error for reconnect=%v: %v", reconnect, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("production publish still blocked after observed resend")
	}
	if !reconnect {
		if lost.Load() != 1 || connections.Load() != 1 || delivered.Load() != 1 {
			t.Fatal("candidate delivered/reconnected unexpectedly", delivered.Load(), lost.Load(), connections.Load())
		}
		t.Log("ISOLATED_CANDIDATE_AUTORECONNECT_DISABLED_ONE_REAL_DELIVERY_DROPPED_BROKER_PUBACK_PRODUCTION_PUBLISH_RETURNS_ERROR_NO_LIBRARY_RESEND; NOT product default, HTTP handler, durable recovery or rollback acceptance")
		return
	}
	if lost.Load() != 1 || connections.Load() < 2 {
		t.Fatal("fault not established", lost.Load(), connections.Load())
	}
	t.Logf("OPEN_GAP_ONE_PRODUCTION_OPSPUBLISH_CALL_DROPPED_REAL_BROKER_PUBACK_RECONNECT_DUPLICATE_OBSERVER_DELIVERIES=%d verified_mtls_connections=%d dropped_puback=%d; no executor/device, no Apply receipt/idempotency/physical-write proof", delivered.Load(), connections.Load(), lost.Load())
}

// Only the existing test hook selects this candidate. No executor subscribes.
func verifyApplyOnceCandidateHandlerPUBACKLoss(t *testing.T, port, ca, gc, gk, ac, ak string, keepOpen bool) {
	t.Helper()
	s, _ := testServer(t)
	const tenant = "itest-mqpipe"
	seed(t, s, tenant)
	if _, err := s.st.Pool.Exec(t.Context(), `DELETE FROM gateway_config_pushes WHERE tenant_id=$1`, tenant); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.st.Pool.Exec(context.Background(), `DELETE FROM gateway_config_pushes WHERE tenant_id=$1`, tenant)
	})
	t.Setenv("FLEET_SIGNING_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	options := func(id, cert, key string) *mqtt.ClientOptions {
		o, e := mqttconfig.Options(func(k string) string {
			return map[string]string{"MQTT_TLS": "true", "MQTT_HOST": "127.0.0.1", "MQTT_PORT": port, "MQTT_CA_FILE": ca, "MQTT_CERT_FILE": cert, "MQTT_KEY_FILE": key}[k]
		}, id)
		if e != nil {
			t.Fatal(e)
		}
		return o
	}
	observer := mqtt.NewClient(options("apply-once-envelope-observer", gc, gk))
	token := observer.Connect()
	if !token.WaitTimeout(5*time.Second) || token.Error() != nil {
		t.Fatal("observer connect", token.Error())
	}
	defer observer.Disconnect(100)
	topic := "t/itest-mqpipe/g/itest-mqpipe-gw/config"
	var deliveries, calls, connections, lost atomic.Int32
	var drop atomic.Bool
	observed := make(chan configPushEnvelope, 4)
	token = observer.Subscribe(topic, 1, func(_ mqtt.Client, m mqtt.Message) {
		var envelope configPushEnvelope
		if json.Unmarshal(m.Payload(), &envelope) == nil {
			deliveries.Add(1)
			select {
			case observed <- envelope:
			default:
			}
		}
	})
	if !token.WaitTimeout(5*time.Second) || token.Error() != nil {
		t.Fatal("subscribe", token.Error())
	}
	opts := options("apply-once-candidate-api", ac, ak)
	opts.SetCustomOpenConnectionFn(func(uri *url.URL, o mqtt.ClientOptions) (net.Conn, error) {
		conf := o.TLSConfig.Clone()
		conf.ServerName = uri.Hostname()
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", uri.Host, conf)
		if err != nil {
			return nil, err
		}
		connections.Add(1)
		return &lostBrokerPUBACKConn{Conn: conn, drop: &drop, lost: &lost, keepOpen: keepOpen}, nil
	})
	drop.Store(true)
	s.opsPublishHook = func(got string, b []byte) error {
		calls.Add(1)
		if got != topic {
			return io.ErrUnexpectedEOF
		}
		return publishApplyOneConnection(t.Context(), opts, got, b, time.Second)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/gateways/{id}/config-push", s.pushGatewayConfig)
	mux.HandleFunc("GET /v1/gateways/{id}/config-push", s.listConfigPushes)
	path := "/v1/gateways/itest-mqpipe-gw/config-push"
	first := call(mux, tenant, "admin", "POST", path, `{}`)
	if first.Code != 502 {
		t.Fatal("actual ack-loss response", first.Code, first.Body.String())
	}
	var got configPushEnvelope
	select {
	case got = <-observed:
	case <-time.After(3 * time.Second):
		t.Fatal("actual broker delivery not observed")
	}
	if got.PushID == "" || got.TenantID != tenant || got.Serial != "SER-itest-mqpipe" {
		t.Fatal("envelope identity", got.PushID, got.TenantID, got.Serial)
	}
	if _, err := s.st.Pool.Exec(t.Context(), `UPDATE gateway_config_pushes SET created_at=now()-interval '1 day' WHERE id=$1`, got.PushID); err != nil {
		t.Fatal(err)
	}
	// A replacement API instance must use durable state, not the candidate's client.
	fresh := &server{st: s.st}
	fresh.opsPublishHook = s.opsPublishHook
	again := http.NewServeMux()
	again.HandleFunc("POST /v1/gateways/{id}/config-push", fresh.pushGatewayConfig)
	again.HandleFunc("GET /v1/gateways/{id}/config-push", fresh.listConfigPushes)
	listed := call(again, tenant, "admin", "GET", path, "")
	second := call(again, tenant, "admin", "POST", path, `{}`)
	var rows []struct {
		ID      string `json:"id"`
		Unknown bool   `json:"publication_unconfirmed"`
		Status  string `json:"status"`
	}
	if listed.Code != 200 || json.Unmarshal(listed.Body.Bytes(), &rows) != nil || len(rows) != 1 || rows[0].ID != got.PushID || !rows[0].Unknown || rows[0].Status != "sent" || second.Code != 409 {
		t.Fatal("durable quarantine", listed.Code, listed.Body.String(), second.Code)
	}
	if calls.Load() != 1 || connections.Load() != 1 || lost.Load() != 1 || deliveries.Load() != 1 || !opts.AutoReconnect {
		t.Fatal("candidate attempts/options", calls.Load(), connections.Load(), lost.Load(), deliveries.Load(), opts.AutoReconnect)
	}
	t.Logf("keep_socket_open=%v ISOLATED_APPLY_CANDIDATE_REAL_MTLS_PUBACK_LOSS_ONE_SIGNED_ENVELOPE_ONE_CONNECTION_HANDLER502_DURABLE_UNKNOWN_DAYOLD_NEW_API_GET_POST409_NO_RESEND; test hook only, no real edge receipt/physical application/default activation", keepOpen)
}

func verifyApplyCandidateDirectTLS(t *testing.T, port, ca, gc, gk, ac, ak string) {
	t.Helper()
	for k, v := range map[string]string{"MQTT_TLS": "true", "MQTT_HOST": "127.0.0.1", "MQTT_PORT": port, "MQTT_CA_FILE": ca, "MQTT_CERT_FILE": ac, "MQTT_KEY_FILE": ak} {
		t.Setenv(k, v)
	}
	// This path uses the actual environment builder, direct dial and TLS handshake,
	// without the connection fault callback used by the ACK-loss scenarios.
	opts, err := mqttconfig.Options(func(k string) string {
		return map[string]string{"MQTT_TLS": "true", "MQTT_HOST": "127.0.0.1", "MQTT_PORT": port, "MQTT_CA_FILE": ca, "MQTT_CERT_FILE": gc, "MQTT_KEY_FILE": gk}[k]
	}, "apply-direct-tls-observer")
	if err != nil {
		t.Fatal(err)
	}
	observer := mqtt.NewClient(opts)
	token := observer.Connect()
	if !token.WaitTimeout(5*time.Second) || token.Error() != nil {
		t.Fatal("connect", token.Error())
	}
	defer observer.Disconnect(100)
	var count atomic.Int32
	seen := make(chan struct{}, 2)
	topic := "t/itest-mqpipe/g/itest-mqpipe-gw/config"
	body := []byte(`{"synthetic_read_only_observer":true,"candidate_direct_tls":true}`)
	token = observer.Subscribe(topic, 1, func(_ mqtt.Client, m mqtt.Message) {
		if bytes.Equal(body, m.Payload()) {
			count.Add(1)
			seen <- struct{}{}
		}
	})
	if !token.WaitTimeout(5*time.Second) || token.Error() != nil {
		t.Fatal("subscribe", token.Error())
	}
	if err = publishApplyMQTTOnceCandidate(t.Context(), topic, body); err != nil {
		t.Fatal(err)
	}
	select {
	case <-seen:
	case <-time.After(3 * time.Second):
		t.Fatal("ACK returned without expected observer delivery")
	}
	if count.Load() != 1 {
		t.Fatal("unexpected delivery count", count.Load())
	}

	bad, err := mqttconfig.Options(os.Getenv, "apply-candidate-hostname-rejected")
	if err != nil {
		t.Fatal(err)
	}
	bad.TLSConfig.ServerName = "intentionally-wrong.invalid"
	if err = publishApplyOneConnection(t.Context(), bad, topic, body, time.Second); err == nil {
		t.Fatal("wrong broker identity accepted")
	}
	if count.Load() != 1 {
		t.Fatal("publication despite rejected broker certificate")
	}
	t.Log("ISOLATED_CANDIDATE_ACTUAL_ENV_BUILDER_DIRECT_DIAL_VERIFIED_MTLS_ACK_ONE_OBSERVER_DELIVERY_AND_REJECT_WRONG_HOSTNAME; no production activation/application receipt")
}
