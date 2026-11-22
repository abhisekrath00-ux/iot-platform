package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/mqttconfig"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/google/uuid"
)

// Candidate only: deliberately NOT wired into pushGatewayConfig or opsPublish.
// Activation, shared-client policy and update/rollback acceptance remain separate.
func publishApplyMQTTOnceCandidate(ctx context.Context, topic string, payload []byte) error {
	opts, err := mqttconfig.Options(os.Getenv, "api-apply-once-"+uuid.NewString())
	if err != nil {
		return err
	}
	return publishApplyOneConnection(ctx, opts, topic, payload, 5*time.Second)
}

// Owns one ephemeral connection and one non-retained QoS1 Publish invocation.
// Any timeout/error is UNKNOWN, never proof of non-delivery or permission to retry.
// A broker ACK is publication evidence only, never gateway application evidence.
func publishApplyOneConnection(ctx context.Context, base *mqtt.ClientOptions, topic string, payload []byte, timeout time.Duration) error {
	if base == nil || len(base.Servers) != 1 || base.Servers[0] == nil || topic == "" || timeout <= 0 || timeout > 10*time.Second {
		return fmt.Errorf("invalid one-attempt publisher configuration")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Copy instead of mutating options used by a shared or previously-created client.
	opts := *base
	opts.SetAutoReconnect(false).SetConnectRetry(false).SetCleanSession(true).SetResumeSubs(false).SetProtocolVersion(4).SetConnectTimeout(timeout).SetWriteTimeout(timeout)
	// Keep this candidate direct TCP/TLS only. No environment proxy path or hidden
	// protocol fallback may create an unbounded connection or a second attempt.
	var mu sync.Mutex
	var owned net.Conn
	testOpen := opts.CustomOpenConnectionFn
	opts.SetCustomOpenConnectionFn(func(uri *url.URL, o mqtt.ClientOptions) (net.Conn, error) {
		var conn net.Conn
		var err error
		if testOpen != nil {
			// Fault-injection seam supplied only by isolated tests. It must respect the
			// supplied ConnectTimeout; production mqttconfig does not supply a callback.
			conn, err = testOpen(uri, o)
		} else {
			conn, err = openApplyOnceDirect(ctx, uri, o.TLSConfig, timeout)
		}
		if err == nil {
			mu.Lock()
			owned = conn
			mu.Unlock()
		}
		return conn, err
	})
	client := mqtt.NewClient(&opts)
	defer func() {
		// Disconnect(0) itself returns before asynchronous Paho cleanup. Close our
		// socket synchronously instead of leaving a live client behind. Already-sent
		// bytes may still arrive; any uncertain outcome must remain quarantined.
		mu.Lock()
		if owned != nil {
			owned.Close()
		}
		mu.Unlock()
		client.Disconnect(0)
	}()
	connection := client.Connect()
	select {
	case <-connection.Done():
	case <-ctx.Done():
		// Wait for MQTT handshake completion, then synchronously close the socket.
		// Dial/TLS are context-bound; MQTT CONNACK uses the finite ConnectTimeout.
		// No Publish is enqueued during this cancellation path.
		connection.Wait()
		return ctx.Err()
	}
	if err := connection.Error(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	publication := client.Publish(topic, 1, false, payload)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-publication.Done():
		return publication.Error()
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("publication acknowledgement unconfirmed")
	}
}

// The validated builder emits only tcp or ssl. Direct dialing avoids Paho's
// proxy branches, whose deadlines differ from its MQTT handshake timeout.
func openApplyOnceDirect(ctx context.Context, uri *url.URL, cfg *tls.Config, timeout time.Duration) (net.Conn, error) {
	if uri == nil || (uri.Scheme != "tcp" && uri.Scheme != "ssl") {
		return nil, fmt.Errorf("unsupported direct Apply transport")
	}
	phase, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(phase, "tcp", uri.Host)
	if err != nil {
		return nil, err
	}
	if uri.Scheme == "tcp" {
		return conn, nil
	}
	if cfg == nil {
		conn.Close()
		return nil, fmt.Errorf("missing Apply TLS configuration")
	}
	copied := cfg.Clone()
	if copied.ServerName == "" {
		copied.ServerName = uri.Hostname()
	}
	secure := tls.Client(conn, copied)
	if err = secure.HandshakeContext(phase); err != nil {
		conn.Close()
		return nil, err
	}
	return secure, nil
}
