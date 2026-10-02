package main

import (
	"bytes"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCfg(t *testing.T, body string) string {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckConfigExitCodes(t *testing.T) {
	var b bytes.Buffer
	if rc := checkConfig(filepath.Join(t.TempDir(), "missing.yaml"), t.TempDir(), &b); rc != 1 {
		t.Fatalf("missing file rc=%d", rc)
	}
	good := writeCfg(t, "gateway_id: g\ntenant_id: t\nmqtt: {host: localhost}\ndevices: []\n")
	b.Reset()
	if rc := checkConfig(good, t.TempDir(), &b); rc != 0 || !strings.Contains(b.String(), "OK") {
		t.Fatalf("good rc=%d out=%s", rc, b.String())
	}
	bad := writeCfg(t, "gateway_id: g\ntenant_id: t\n")
	b.Reset()
	if rc := checkConfig(bad, t.TempDir(), &b); rc != 1 || !strings.Contains(b.String(), "mqtt.host") {
		t.Fatalf("bad rc=%d out=%s", rc, b.String())
	}
}

func TestHealthCheck(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	connected := false
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if connected {
			w.Write([]byte(`{"version":"v1","connected":true,"queue_depth":0,"uptime_seconds":5}`))
		} else {
			w.Write([]byte(`{"version":"v1","connected":false,"queue_depth":7,"uptime_seconds":5}`))
		}
	})}
	go srv.Serve(ln)
	defer srv.Close()
	cfg := writeCfg(t, "gateway_id: g\ntenant_id: t\nmqtt: {host: localhost}\nui: {listen: \""+ln.Addr().String()+"\"}\n")
	var b bytes.Buffer
	if rc := healthCheck(cfg, &b); rc != 2 || !strings.Contains(b.String(), "DEGRADED") {
		t.Fatalf("rc=%d %s", rc, b.String())
	}
	connected = true
	b.Reset()
	if rc := healthCheck(cfg, &b); rc != 0 || !strings.HasPrefix(b.String(), "OK") {
		t.Fatalf("rc=%d %s", rc, b.String())
	}
	srv.Close()
	b.Reset()
	if rc := healthCheck(cfg, &b); rc != 1 {
		t.Fatalf("down rc=%d %s", rc, b.String())
	}
}
