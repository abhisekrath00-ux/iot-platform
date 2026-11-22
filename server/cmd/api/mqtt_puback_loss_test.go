package main

import (
	"bytes"
	"crypto/tls"
	"io"
	"net"
	"net/url"
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
	drop    *atomic.Bool
	lost    *atomic.Int32
	pending []byte
}

func (c *lostBrokerPUBACKConn) Read(b []byte) (int, error) {
	if len(c.pending) == 0 {
		packet, err := packets.ReadPacket(c.Conn)
		if err != nil {
			return 0, err
		}
		if _, ok := packet.(*packets.PubackPacket); ok && c.drop.CompareAndSwap(true, false) {
			c.lost.Add(1)
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
