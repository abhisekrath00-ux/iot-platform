package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

type coilFakeWriter struct{ got map[string]float64 }

func (f *coilFakeWriter) Write(_ context.Context, p string, v float64) error {
	f.got[p] = v
	return nil
}

func TestGPIOAndCoilSetters(t *testing.T) {
	p := filepath.Join(t.TempDir(), "value")
	set := gpioSetter(config.Output{Path: p})
	set(true)
	if b, _ := os.ReadFile(p); string(b) != "1\n" {
		t.Fatalf("%q", b)
	}
	set(false)
	if b, _ := os.ReadFile(p); string(b) != "0\n" {
		t.Fatalf("%q", b)
	}
	low := gpioSetter(config.Output{Path: p, ActiveLow: true})
	low(true)
	if b, _ := os.ReadFile(p); string(b) != "0\n" {
		t.Fatalf("active_low on = %q", b)
	}
	fw := &coilFakeWriter{got: map[string]float64{}}
	writers.reset()
	writers.set("relay1", fw)
	cs := coilSetter(config.Output{Device: "relay1", Point: "siren"})
	cs(true)
	if fw.got["siren"] != 1 {
		t.Fatalf("%v", fw.got)
	}
	if err := coilSetter(config.Output{Device: "nope", Point: "x"})(true); err == nil {
		t.Fatal("wrote to an unregistered device")
	}
}

func TestValidateRulesForChecksCoilAllowlistAndLocalFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "edge-agent.yaml")
	cfg := &config.Config{
		Outputs: []config.Output{{Name: "siren", Class: "alarm", Kind: "modbus_coil", Device: "relay1", Point: "siren"}},
		Rules:   []config.Rule{{ID: "r1", Type: "link_down", For: 30 * time.Second, Output: "siren"}},
		Devices: []config.Device{{ID: "relay1", Writes: []config.Point{{ID: "siren", Min: 0, Max: 1}}}},
	}
	if err := validateRulesFor(cfg, cfgPath); err != nil {
		t.Fatalf("valid: %v", err)
	}
	cfg.Devices[0].Writes[0].Max = 100 // not a coil-range allowlist entry
	if err := validateRulesFor(cfg, cfgPath); err == nil || !strings.Contains(err.Error(), "min 0 / max 1") {
		t.Fatalf("wide write range accepted: %v", err)
	}
	cfg.Devices[0].Writes[0].Max = 1
	// operator-added rule that names an output outside the allowlist is refused
	os.WriteFile(filepath.Join(dir, "local-rules.yaml"), []byte("rules:\n  - id: l1\n    type: link_down\n    output: pump\n"), 0o600)
	if err := validateRulesFor(cfg, cfgPath); err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("local rule drove a non-allowlisted output: %v", err)
	}
	// and one that reuses a pushed rule id
	os.WriteFile(filepath.Join(dir, "local-rules.yaml"), []byte("rules:\n  - id: r1\n    type: link_down\n    output: siren\n"), 0o600)
	if err := validateRulesFor(cfg, cfgPath); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate id accepted: %v", err)
	}
}

// End to end without any broker: link is down, so the GPIO file goes to 1;
// when the link returns it goes back to 0.
func TestOfflineSirenEndToEnd(t *testing.T) {
	dir := t.TempDir()
	gpio := filepath.Join(dir, "gpio17")
	cfg := &config.Config{
		Outputs: []config.Output{{Name: "siren", Class: "alarm", Kind: "gpio_file", Path: gpio}},
		Rules:   []config.Rule{{ID: "offline", Type: "link_down", Output: "siren"}},
	}
	var up atomicBool
	ctx, cancel := context.WithCancel(context.Background())
	e := startLocalRules(ctx, cfg, filepath.Join(dir, "edge-agent.yaml"), dir, up.get)
	if e == nil {
		t.Fatal("engine not started")
	}
	wait := func(want string) {
		t.Helper()
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			if b, _ := os.ReadFile(gpio); string(b) == want {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		b, _ := os.ReadFile(gpio)
		t.Fatalf("gpio = %q, want %q", b, want)
	}
	wait("1\n")
	up.set(true)
	wait("0\n")
	cancel()
	rulesRT.mu.Lock()
	d := rulesRT.done
	rulesRT.mu.Unlock()
	<-d
	if b, _ := os.ReadFile(gpio); string(b) != "0\n" {
		t.Fatalf("siren left on after shutdown: %q", b)
	}
}

type atomicBool struct{ v atomic.Bool }

func (a *atomicBool) get() bool  { return a.v.Load() }
func (a *atomicBool) set(b bool) { a.v.Store(b) }
