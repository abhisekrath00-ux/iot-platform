package main

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/mqttconfig"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/google/uuid"
	"golang.org/x/net/proxy"
)

// Commands use a one-shot connection, not the shared reconnecting retained
// publisher. A timeout is uncertain delivery, never permission to retry.
func (s *server) publishCommand(ctx context.Context, topic string, body []byte) error {
	opts, err := mqttconfig.Options(os.Getenv, "api-command-"+uuid.NewString())
	if err != nil {
		return err
	}
	return publishCommandOnce(ctx, opts, topic, body, 15*time.Second)
}

func publishCommandOnce(parent context.Context, opts *mqtt.ClientOptions, topic string, body []byte, limit time.Duration) error {
	ctx, cancel := context.WithTimeout(parent, limit)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	opts.SetAutoReconnect(false).SetConnectRetry(false).SetCleanSession(true).SetWriteTimeout(limit).SetConnectTimeout(limit)
	// The custom dial keeps the actual socket under this attempt's deadline and
	// cancellation, including a broker that never returns CONNACK or PUBACK.
	// Preserve configured proxy behavior, verified TLS roots and hostname checks.
	var mu sync.Mutex
	var socket net.Conn
	closeSocket := func() {
		mu.Lock()
		defer mu.Unlock()
		if socket != nil {
			_ = socket.Close()
		}
	}
	stop := context.AfterFunc(ctx, closeSocket)
	defer func() { stop(); closeSocket() }()
	opts.SetCustomOpenConnectionFn(func(uri *url.URL, cfg mqtt.ClientOptions) (net.Conn, error) {
		dialer := proxy.FromEnvironmentUsing(cfg.Dialer)
		cd, ok := dialer.(proxy.ContextDialer)
		if !ok {
			return nil, errors.New("command MQTT proxy lacks context-aware dialing")
		}
		conn, err := cd.DialContext(ctx, "tcp", uri.Host)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		socket = conn
		mu.Unlock()
		if err := ctx.Err(); err != nil {
			closeSocket()
			return nil, err
		}
		if deadline, ok := ctx.Deadline(); ok {
			if err = conn.SetDeadline(deadline); err != nil {
				closeSocket()
				return nil, err
			}
		}
		switch uri.Scheme {
		case "ssl":
			cfgTLS := cfg.TLSConfig.Clone()
			if cfgTLS.ServerName == "" {
				cfgTLS.ServerName = uri.Hostname()
			}
			secure := tls.Client(conn, cfgTLS)
			if err = secure.HandshakeContext(ctx); err != nil {
				closeSocket()
				return nil, err
			}
			return secure, nil
		case "tcp":
			return conn, nil
		default:
			closeSocket()
			return nil, errors.New("unsupported command MQTT transport")
		}
	})
	client := mqtt.NewClient(opts)
	defer client.Disconnect(0)
	wait := func(token mqtt.Token) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-token.Done():
			return token.Error()
		}
	}
	if err := wait(client.Connect()); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return wait(client.Publish(topic, 1, false, body))
}
