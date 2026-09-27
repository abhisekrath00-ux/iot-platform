// Package mqttc wraps the MQTT client: mTLS, per-gateway identity,
// telemetry publish with ACK, and command subscription.
package mqttc

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type Client struct{ c mqtt.Client }

func Connect(cfg *config.Config, onCommand mqtt.MessageHandler) (*Client, error) {
	opts := mqtt.NewClientOptions().
		SetClientID(firstNonEmpty(cfg.MQTT.ClientID, cfg.GatewayID)).
		SetAutoReconnect(true).
		SetConnectRetry(true)

	scheme := "tcp"
	if cfg.MQTT.TLS {
		scheme = "ssl"
		tlsCfg, err := clientTLS(cfg)
		if err != nil {
			return nil, err
		}
		opts.SetTLSConfig(tlsCfg)
	}
	opts.AddBroker(fmt.Sprintf("%s://%s:%d", scheme, cfg.MQTT.Host, cfg.MQTT.Port))

	c := mqtt.NewClient(opts)
	if tok := c.Connect(); tok.Wait() && tok.Error() != nil {
		return nil, fmt.Errorf("mqtt connect: %w", tok.Error())
	}
	cmdTopic := fmt.Sprintf("t/%s/g/%s/cmd", cfg.TenantID, cfg.GatewayID)
	if tok := c.Subscribe(cmdTopic, 1, onCommand); tok.Wait() && tok.Error() != nil {
		return nil, fmt.Errorf("mqtt subscribe: %w", tok.Error())
	}
	return &Client{c: c}, nil
}

// Subscribe registers a handler on an additional topic at QoS 1.
func (c *Client) Subscribe(topic string, handler mqtt.MessageHandler) error {
	tok := c.c.Subscribe(topic, 1, handler)
	tok.Wait()
	return tok.Error()
}

func (c *Client) Publish(topic string, payload []byte) error {
	tok := c.c.Publish(topic, 1, false, payload)
	tok.Wait()
	return tok.Error()
}

func (c *Client) Close() { c.c.Disconnect(250) }

func clientTLS(cfg *config.Config) (*tls.Config, error) {
	ca, err := os.ReadFile(cfg.MQTT.CAFile)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("bad CA file")
	}
	cert, err := tls.LoadX509KeyPair(cfg.MQTT.CertFile, cfg.MQTT.KeyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		RootCAs:      pool,
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
