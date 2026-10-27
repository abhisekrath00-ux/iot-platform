package mqttconfig

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
func TestOptionsLabAndReject(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
		o, e := Options(env(map[string]string{"MQTT_HOST": host}), "synthetic")
		if e != nil || o.Servers[0].Scheme != "tcp" || o.Servers[0].Port() != "1883" || o.ClientID != "synthetic" {
			t.Fatalf("lab %s: %v %v", host, o, e)
		}
	}
	for _, m := range []map[string]string{{}, {"MQTT_HOST": "tcp://localhost"}, {"MQTT_HOST": "host/path"}, {"MQTT_HOST": "host", "MQTT_PORT": "70000"}, {"MQTT_HOST": "host", "MQTT_TLS": "yes"}, {"MQTT_HOST": "host", "MQTT_TLS": "true"}, {"MQTT_HOST": "host", "MQTT_CERT_FILE": "synthetic-secret-path"}} {
		if _, e := Options(env(m), "test"); e == nil || strings.Contains(e.Error(), "synthetic-secret-path") {
			t.Fatalf("not rejected/sensitive: %v %v", m, e)
		}
	}
}

func TestOptionsTLSValidation(t *testing.T) {
	dir := t.TempDir()
	issue := mqttTestCerts(t, dir)
	cert, key := issue("ingest", false)
	m := map[string]string{"MQTT_HOST": "localhost", "MQTT_TLS": "true", "MQTT_CA_FILE": dir + "/ca.crt", "MQTT_CERT_FILE": cert, "MQTT_KEY_FILE": key}
	o, e := Options(env(m), "service")
	if e != nil || o.Servers[0].Scheme != "ssl" || o.Servers[0].Port() != "8883" || o.TLSConfig.InsecureSkipVerify || len(o.TLSConfig.Certificates) != 1 {
		t.Fatalf("TLS config %v %v", o, e)
	}
	for _, field := range []string{"MQTT_CA_FILE", "MQTT_CERT_FILE", "MQTT_KEY_FILE"} {
		old := m[field]
		m[field] = dir + "/missing"
		if _, e := Options(env(m), "service"); e == nil {
			t.Fatal("missing TLS field accepted", field)
		}
		m[field] = old
	}
	m["MQTT_CA_FILE"] = key
	if _, e := Options(env(m), "service"); e == nil {
		t.Fatal("key accepted as CA")
	}
	m["MQTT_CA_FILE"] = dir + "/ca.crt"
	m["MQTT_KEY_FILE"] = cert
	if _, e := Options(env(m), "service"); e == nil {
		t.Fatal("mismatched certificate key accepted")
	}
}
