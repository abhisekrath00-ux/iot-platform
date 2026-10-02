package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/localrules"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/paths"
)

// ruleObs feeds device readings to the running local-rules engine.
var ruleObs atomic.Pointer[localrules.Engine]

var rulesRT struct {
	mu          sync.Mutex
	cancel      context.CancelFunc
	done        chan struct{}
	parent      context.Context
	cfgPath     string
	identityDir string
	link        func() bool
}

func controlPath(identityDir string) string {
	if identityDir == "" {
		identityDir = paths.Data()
	}
	return filepath.Join(identityDir, "rules-control.json")
}

func gpioSetter(o config.Output) localrules.Setter {
	return func(on bool) error {
		v := "0\n"
		if on != o.ActiveLow {
			v = "1\n"
		}
		return os.WriteFile(o.Path, []byte(v), 0o644)
	}
}

// coilSetter drives a Modbus coil through the same per-device `writes`
// allowlist and range checks as commands; the point must be listed there.
func coilSetter(o config.Output) localrules.Setter {
	return func(on bool) error {
		w, ok := writers.get(o.Device)
		if !ok {
			return fmt.Errorf("device %s is not writable (not connected or no writes allowlist)", o.Device)
		}
		v := 0.0
		if on {
			v = 1
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return w.Write(ctx, o.Point, v)
	}
}

func buildRules(cfg *config.Config, cfgPath string) ([]config.Rule, error) {
	local, err := config.LoadLocalRules(filepath.Join(filepath.Dir(cfgPath), "local-rules.yaml"))
	if err != nil {
		return nil, err
	}
	return append(append([]config.Rule(nil), cfg.Rules...), local...), nil
}

// ValidateRulesFor is used before a pushed config is installed.
func validateRulesFor(cfg *config.Config, cfgPath string) error {
	rules, err := buildRules(cfg, cfgPath)
	if err != nil {
		return err
	}
	if errs := localrules.Validate(cfg.Outputs, rules); len(errs) > 0 {
		return fmt.Errorf("%v", errs)
	}
	for _, o := range cfg.Outputs {
		if o.Kind != "modbus_coil" {
			continue
		}
		found := false
		for _, d := range cfg.Devices {
			if d.ID != o.Device {
				continue
			}
			for _, w := range d.Writes {
				if w.ID == o.Point && w.Min == 0 && w.Max == 1 {
					found = true
				}
			}
		}
		if !found {
			return fmt.Errorf("output %s: device %s has no write point %q with min 0 / max 1", o.Name, o.Device, o.Point)
		}
	}
	return nil
}

// startLocalRules starts (or restarts) the engine. It never depends on the
// broker connection. Invalid rules stop the agent at boot on purpose: a
// silently disabled alarm is worse than a loud failure at install time.
func startLocalRules(ctx context.Context, cfg *config.Config, cfgPath, identityDir string, link func() bool) *localrules.Engine {
	rulesRT.mu.Lock()
	rulesRT.parent, rulesRT.cfgPath, rulesRT.identityDir, rulesRT.link = ctx, cfgPath, identityDir, link
	rulesRT.mu.Unlock()
	e, err := restartLocalRules(cfg)
	if err != nil {
		log.Fatalf("local rules: %v", err)
	}
	return e
}

func restartLocalRules(cfg *config.Config) (*localrules.Engine, error) {
	rulesRT.mu.Lock()
	defer rulesRT.mu.Unlock()
	if rulesRT.cancel != nil {
		rulesRT.cancel()
		<-rulesRT.done // old engine switched everything off
		rulesRT.cancel = nil
		ruleObs.Store(nil)
	}
	if len(cfg.Outputs) == 0 && len(cfg.Rules) == 0 {
		return nil, nil
	}
	if err := validateRulesFor(cfg, rulesRT.cfgPath); err != nil {
		return nil, err
	}
	rules, _ := buildRules(cfg, rulesRT.cfgPath)
	setters := map[string]localrules.Setter{}
	for _, o := range cfg.Outputs {
		switch o.Kind {
		case "gpio_file":
			setters[o.Name] = gpioSetter(o)
		case "modbus_coil":
			setters[o.Name] = coilSetter(o)
		case "simulate":
			name := o.Name
			setters[name] = func(on bool) error { log.Printf("output %s (simulated): on=%v", name, on); return nil }
		}
	}
	e, err := localrules.New(cfg.Outputs, rules, setters, rulesRT.link, nil)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(rulesRT.parent)
	rulesRT.cancel, rulesRT.done = cancel, make(chan struct{})
	done := rulesRT.done
	ruleObs.Store(e)
	go func() { defer close(done); e.Run(ctx, controlPath(rulesRT.identityDir)) }()
	log.Printf("local rules: %d rules, %d outputs (run offline; %d from this config)", len(rules), len(cfg.Outputs), len(cfg.Rules))
	return e, nil
}
