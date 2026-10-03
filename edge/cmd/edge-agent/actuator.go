package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/cmdexec"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/driver"
)

// writerRegistry maps device ids to the live driver that may write to them.
// The supervisor refills it on every (re)load, so a reload never leaves a
// stale driver reachable.
type writerRegistry struct {
	mu sync.RWMutex
	m  map[string]driver.Writer
}

func (r *writerRegistry) reset() {
	r.mu.Lock()
	r.m = map[string]driver.Writer{}
	r.mu.Unlock()
}

func (r *writerRegistry) set(id string, w driver.Writer) {
	r.mu.Lock()
	if r.m == nil {
		r.m = map[string]driver.Writer{}
	}
	r.m[id] = w
	r.mu.Unlock()
}

func (r *writerRegistry) get(id string) (driver.Writer, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	w, ok := r.m[id]
	return w, ok
}

// modbusActuator executes "modbus.write" envelopes. By the time Execute runs the
// envelope has already passed cmdexec.Gate (approved by a second person,
// unexpired, not replayed, action allowlisted for this gateway). The driver then
// enforces the per-device register allowlist and value range.
type modbusActuator struct {
	reg *writerRegistry
	// auto decides whether an AUTOMATIC command (no human approval) may drive device/point.
	// nil refuses every automatic command. It is built from this gateway's own config only.
	auto func(device, point string) bool
}

const actionModbusWrite = "modbus.write"

func (a *modbusActuator) Execute(ctx context.Context, e *cmdexec.Envelope) (string, error) {
	if e.Action != actionModbusWrite {
		return "", fmt.Errorf("unsupported action %q", e.Action)
	}
	var p struct {
		Point string   `json:"point"`
		Value *float64 `json:"value"`
	}
	dec := json.NewDecoder(bytes.NewReader(e.Parameters))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil || p.Point == "" || p.Value == nil {
		return "", fmt.Errorf("parameters must be {point, value}")
	}
	if e.Mode == cmdexec.ModeAutomatic {
		// The edge is the last gate for commands nobody approved: only a configured alarm output,
		// only on or off.
		if a.auto == nil || !a.auto(e.Target, p.Point) {
			return "", fmt.Errorf("automatic commands are not allowed for %s.%s on this gateway", e.Target, p.Point)
		}
		if *p.Value != 0 && *p.Value != 1 {
			return "", fmt.Errorf("automatic commands may only switch an output on (1) or off (0)")
		}
	}
	w, ok := a.reg.get(e.Target)
	if !ok {
		return "", fmt.Errorf("device %q is not writable on this gateway", e.Target)
	}
	if err := w.Write(ctx, p.Point, *p.Value); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %v to %s.%s (device echo verified)", *p.Value, e.Target, p.Point), nil
}

// autoAllowed builds the edge-side rule for automatic commands from the gateway's own config: the
// operator must opt in (allow_automatic_commands) AND the device/point must be a configured alarm
// output backed by a Modbus coil. The server cannot widen this.
func autoAllowed(allow bool, outputs []config.Output) func(device, point string) bool {
	if !allow {
		return nil
	}
	return func(device, point string) bool {
		for _, o := range outputs {
			if o.Class == "alarm" && o.Kind == "modbus_coil" && o.Device == device && o.Point == point {
				return true
			}
		}
		return false
	}
}
