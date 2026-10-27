// Package mqttconfig builds the server services' MQTT transport configuration.
// TLS is opt-in for existing labs; when requested, invalid credentials fail closed.
package mqttconfig

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

func Options(get func(string) string, clientID string) (*mqtt.ClientOptions, error) {
	host := strings.TrimSpace(get("MQTT_HOST"))
	if host == "" || strings.ContainsAny(host, "/@?#\\ \t\r\n") {
		return nil, fmt.Errorf("MQTT_HOST must be a hostname or IP")
	}
	if strings.Contains(host, ":") && net.ParseIP(host) == nil {
		return nil, fmt.Errorf("MQTT_HOST must not include a port")
	}
	mode := get("MQTT_TLS")
	if mode != "" && mode != "false" && mode != "true" {
		return nil, fmt.Errorf("MQTT_TLS must be true or false")
	}
	secure := mode == "true"
	port := get("MQTT_PORT")
	if port == "" {
		if secure {
			port = "8883"
		} else {
			port = "1883"
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return nil, fmt.Errorf("MQTT_PORT must be 1..65535")
	}
	scheme := "tcp"
	if secure {
		scheme = "ssl"
	}
	u := url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port)}
	opts := mqtt.NewClientOptions().AddBroker(u.String()).SetClientID(clientID).SetAutoReconnect(true).SetConnectTimeout(10 * time.Second)
	if secure {
		ca, err := os.ReadFile(get("MQTT_CA_FILE"))
		if err != nil {
			return nil, fmt.Errorf("MQTT_CA_FILE unreadable")
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(ca) {
			return nil, fmt.Errorf("MQTT_CA_FILE contains no certificates")
		}
		cert, err := tls.LoadX509KeyPair(get("MQTT_CERT_FILE"), get("MQTT_KEY_FILE"))
		if err != nil {
			return nil, fmt.Errorf("MQTT client certificate/key invalid or unreadable")
		}
		opts.SetTLSConfig(&tls.Config{RootCAs: roots, Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	} else if get("MQTT_CA_FILE") != "" || get("MQTT_CERT_FILE") != "" || get("MQTT_KEY_FILE") != "" {
		return nil, fmt.Errorf("MQTT certificate settings require MQTT_TLS=true")
	}
	return opts, nil
}
