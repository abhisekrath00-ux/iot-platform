package config

import (
	"strings"
	"testing"
)

func TestLintPort(t *testing.T) {
	cases := []struct {
		goos, port string
		bad        bool
	}{
		{"windows", "COM3", false}, {"windows", `\\.\COM12`, false}, {"windows", "/dev/ttyUSB0", true},
		{"linux", "/dev/ttyUSB0", false}, {"linux", "/dev/serial/by-id/usb-x", false}, {"linux", "COM3", true},
	}
	for _, c := range cases {
		if got := LintPort(c.goos, c.port) != ""; got != c.bad {
			t.Errorf("%s %s: bad=%v want %v", c.goos, c.port, got, c.bad)
		}
	}
}

func TestLintFindings(t *testing.T) {
	var c Config
	c.MQTT.Host = "broker.example"
	c.UI.Listen = "0.0.0.0:8088"
	c.Devices = []Device{
		{ID: "a", Port: "COM3", Address: 300, Points: []Point{{ID: "x"}, {ID: "x"}}},
		{ID: "a"},
	}
	is := Lint(&c, "linux")
	var all []string
	for _, i := range is {
		all = append(all, i.String())
	}
	s := strings.Join(all, "\n")
	for _, want := range []string{"mqtt.tls is off", "exposes the status page", "not a device path", "outside 0-247", `point id "x" is used twice`, `device id "a" is used twice`, "has no points"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing finding %q in:\n%s", want, s)
		}
	}
	if !HasError(is) {
		t.Error("expected errors")
	}
}

func TestLintCleanConfig(t *testing.T) {
	var c Config
	c.MQTT.Host = "localhost"
	c.Devices = []Device{{ID: "m", Port: "/dev/ttyUSB0", Address: 1, Points: []Point{{ID: "kwh"}}}}
	if is := Lint(&c, "linux"); len(is) != 0 {
		t.Fatalf("unexpected: %v", is)
	}
}

func TestLintMissingMQTTHost(t *testing.T) {
	if !HasError(Lint(&Config{}, "linux")) {
		t.Fatal("empty mqtt host must be an error")
	}
}
