// Package localrules runs alarm rules on the edge box itself, so a siren or
// buzzer still sounds when the server, broker or network is gone. It can drive
// only outputs the gateway config allowlists as class "alarm", never exceeds
// each output's max-on time, and fails safe (everything off) on shutdown.
package localrules

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

// Setter switches one physical output.
type Setter func(on bool) error

var idRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,48}$`)

const (
	defaultMaxOn = 10 * time.Minute
	maxMaxOn     = time.Hour
	maxTestFor   = 10 * time.Second
	maxSilence   = 24 * time.Hour
)

// Validate checks outputs and rules together and returns every problem found.
func Validate(outs []config.Output, rules []config.Rule) []error {
	var errs []error
	add := func(f string, a ...any) { errs = append(errs, fmt.Errorf(f, a...)) }
	names := map[string]bool{}
	for _, o := range outs {
		switch {
		case !idRe.MatchString(o.Name):
			add("output %q: name must be 1-48 of letters, digits, . _ -", o.Name)
		case names[o.Name]:
			add("output %s: duplicate name", o.Name)
		}
		names[o.Name] = true
		if o.Class != "alarm" {
			add("output %s: class must be \"alarm\" (local rules may only drive annunciators)", o.Name)
		}
		if o.MaxOn < 0 || o.MaxOn > maxMaxOn {
			add("output %s: max_on must be up to %s", o.Name, maxMaxOn)
		}
		switch o.Kind {
		case "simulate":
		case "gpio_file":
			if len(o.Path) < 2 || o.Path[0] != '/' && (len(o.Path) < 3 || o.Path[1] != ':') {
				add("output %s: gpio_file needs an absolute path", o.Name)
			}
		case "modbus_coil":
			if o.Device == "" || o.Point == "" {
				add("output %s: modbus_coil needs device and point", o.Name)
			}
		default:
			add("output %s: kind must be gpio_file, modbus_coil or simulate", o.Name)
		}
	}
	ids := map[string]bool{}
	for _, r := range rules {
		if !idRe.MatchString(r.ID) {
			add("rule %q: id must be 1-48 of letters, digits, . _ -", r.ID)
			continue
		}
		if ids[r.ID] {
			add("rule %s: duplicate id (pushed and local rules share one namespace)", r.ID)
		}
		ids[r.ID] = true
		if !names[r.Output] {
			add("rule %s: output %q is not in this gateway's outputs allowlist", r.ID, r.Output)
		}
		if r.For < 0 || r.For > 24*time.Hour {
			add("rule %s: for must be 0-24h", r.ID)
		}
		switch r.Pattern {
		case "", "steady", "pulse":
		default:
			add("rule %s: pattern must be steady or pulse", r.ID)
		}
		switch r.Type {
		case "link_down":
		case "threshold":
			switch r.Op {
			case ">", ">=", "<", "<=", "==", "!=":
			default:
				add("rule %s: op must be one of > >= < <= == !=", r.ID)
			}
			if r.Device == "" || r.Point == "" || math.IsNaN(r.Value) || math.IsInf(r.Value, 0) {
				add("rule %s: threshold needs device, point and a finite value", r.ID)
			}
		case "stale":
			if r.Device == "" || r.For < 5*time.Second {
				add("rule %s: stale needs a device and for >= 5s", r.ID)
			}
		default:
			add("rule %s: type must be link_down, threshold or stale", r.ID)
		}
	}
	return errs
}

type sample struct {
	v  float64
	at time.Time
}

type ruleState struct {
	r         config.Rule
	condSince time.Time // zero when the condition is false
	active    bool
}

type outState struct {
	cfg         config.Output
	set         Setter
	on          bool
	onSince     time.Time
	activeSince time.Time
	exhausted   bool
	silenced    time.Time // until
	testUntil   time.Time
	lastErr     string
	pulse       bool
}

// Engine evaluates rules once per Tick. It is safe for concurrent Observe.
type Engine struct {
	mu      sync.Mutex
	rules   []*ruleState
	outs    map[string]*outState
	latest  map[string]sample
	lastDev map[string]time.Time
	start   time.Time
	link    func() bool
	now     func() time.Time
}

// New builds an engine. setters maps output name -> Setter; every configured
// output needs one. link reports whether the broker connection is up.
func New(outs []config.Output, rules []config.Rule, setters map[string]Setter, link func() bool, now func() time.Time) (*Engine, error) {
	if errs := Validate(outs, rules); len(errs) > 0 {
		return nil, fmt.Errorf("local rules: %v", errs)
	}
	if now == nil {
		now = time.Now
	}
	e := &Engine{outs: map[string]*outState{}, latest: map[string]sample{}, lastDev: map[string]time.Time{}, link: link, now: now}
	e.start = now()
	for _, o := range outs {
		set, ok := setters[o.Name]
		if !ok {
			return nil, fmt.Errorf("output %s has no driver", o.Name)
		}
		if o.MaxOn == 0 {
			o.MaxOn = defaultMaxOn
		}
		e.outs[o.Name] = &outState{cfg: o, set: set}
	}
	for _, r := range rules {
		e.rules = append(e.rules, &ruleState{r: r})
	}
	return e, nil
}

// Observe records the latest successful readings of a device.
func (e *Engine) Observe(device string, vals map[string]float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.now()
	e.lastDev[device] = t
	for p, v := range vals {
		e.latest[device+"\x00"+p] = sample{v, t}
	}
}

func (e *Engine) cond(rs *ruleState, now time.Time) bool {
	r := rs.r
	switch r.Type {
	case "link_down":
		return e.link == nil || !e.link()
	case "threshold":
		s, ok := e.latest[r.Device+"\x00"+r.Point]
		if !ok {
			return false
		}
		switch r.Op {
		case ">":
			return s.v > r.Value
		case ">=":
			return s.v >= r.Value
		case "<":
			return s.v < r.Value
		case "<=":
			return s.v <= r.Value
		case "==":
			return s.v == r.Value
		case "!=":
			return s.v != r.Value
		}
	case "stale":
		last, ok := e.lastDev[r.Device]
		if !ok {
			last = e.start
		}
		return now.Sub(last) > r.For
	}
	return false
}

// Tick evaluates every rule and drives the outputs. Call about once a second.
func (e *Engine) Tick() {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	want := map[string]string{} // output -> pattern, for active rules
	for _, rs := range e.rules {
		if e.cond(rs, now) {
			if rs.condSince.IsZero() {
				rs.condSince = now
			}
			hold := rs.r.For
			if rs.r.Type == "stale" {
				hold = 0 // For is already the staleness window
			}
			rs.active = now.Sub(rs.condSince) >= hold
		} else {
			rs.condSince, rs.active = time.Time{}, false
		}
		if rs.active {
			if rs.r.Pattern == "pulse" && want[rs.r.Output] == "" {
				want[rs.r.Output] = "pulse"
			} else {
				want[rs.r.Output] = "steady"
			}
		}
	}
	for name, o := range e.outs {
		pat, active := want[name]
		if !active {
			o.exhausted, o.activeSince = false, time.Time{}
		} else if o.activeSince.IsZero() {
			o.activeSince = now
		}
		// Cap on how long one alarm may sound: after max_on it goes quiet
		// until the alarm clears and fires again.
		if active && now.Sub(o.activeSince) >= o.cfg.MaxOn {
			o.exhausted = true
		}
		on := active && !o.exhausted && !now.Before(o.silenced)
		if on && pat == "pulse" {
			o.pulse = !o.pulse
			on = o.pulse
		}
		if now.Before(o.testUntil) {
			on = true
		}
		e.drive(o, on, now)
	}
}

func (e *Engine) drive(o *outState, on bool, now time.Time) {
	if on == o.on && o.lastErr == "" {
		return
	}
	if err := o.set(on); err != nil {
		o.lastErr = err.Error()
		log.Printf("local rules: output %s: %v", o.cfg.Name, err)
		return
	}
	o.lastErr = ""
	if on && !o.on {
		o.onSince = now
	}
	o.on = on
}

// Silence mutes an output until the given time (max 24h). Rules keep
// evaluating; the output sounds again afterwards if the alarm is still active.
func (e *Engine) Silence(output string, d time.Duration) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	o, ok := e.outs[output]
	if !ok {
		return fmt.Errorf("unknown output %q", output)
	}
	if d <= 0 || d > maxSilence {
		return fmt.Errorf("silence must be 1s-24h")
	}
	o.silenced = e.now().Add(d)
	return nil
}

// Test sounds an output for up to 10 seconds regardless of rules.
func (e *Engine) Test(output string, d time.Duration) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	o, ok := e.outs[output]
	if !ok {
		return fmt.Errorf("unknown output %q", output)
	}
	if d <= 0 || d > maxTestFor {
		d = maxTestFor
	}
	o.testUntil = e.now().Add(d)
	return nil
}

// AllOff switches every output off (shutdown, reload).
func (e *Engine) AllOff() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, o := range e.outs {
		if o.on {
			o.set(false)
			o.on = false
		}
	}
}

// RuleStatus is a snapshot for the local status page and tests.
type RuleStatus struct {
	ID, Output string
	Active     bool
}

// OutputStatus is one output's state.
type OutputStatus struct {
	Name      string
	On        bool
	Silenced  bool
	Exhausted bool
	Error     string
}

func (e *Engine) Status() ([]RuleStatus, []OutputStatus) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	var rs []RuleStatus
	for _, r := range e.rules {
		rs = append(rs, RuleStatus{r.r.ID, r.r.Output, r.active})
	}
	var os []OutputStatus
	for _, o := range e.outs {
		os = append(os, OutputStatus{o.cfg.Name, o.on, now.Before(o.silenced), o.exhausted, o.lastErr})
	}
	sort.Slice(os, func(i, j int) bool { return os[i].Name < os[j].Name })
	return rs, os
}

// control is the operator's local handshake: `edge-agent -silence NAME -for 10m`
// or `-test-output NAME` drops this file in the data directory and the running
// agent consumes it. Only someone with write access to that directory can use it.
type control struct {
	Action string `json:"action"` // silence | test
	Output string `json:"output"`
	For    string `json:"for"`
}

// WriteControl is what the CLI calls.
func WriteControl(path, action, output string, d time.Duration) error {
	b, _ := json.Marshal(control{action, output, d.String()})
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (e *Engine) consumeControl(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	os.Remove(path)
	var c control
	if json.Unmarshal(b, &c) != nil {
		log.Printf("local rules: bad control file ignored")
		return
	}
	d, _ := time.ParseDuration(c.For)
	var err2 error
	switch c.Action {
	case "silence":
		err2 = e.Silence(c.Output, d)
	case "test":
		err2 = e.Test(c.Output, d)
	default:
		err2 = fmt.Errorf("unknown action %q", c.Action)
	}
	if err2 != nil {
		log.Printf("local rules: control %s %s: %v", c.Action, c.Output, err2)
		return
	}
	log.Printf("local rules: %s %s for %s", c.Action, c.Output, d)
}

// Run ticks every second until ctx ends, then switches all outputs off.
func (e *Engine) Run(ctx context.Context, controlPath string) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	defer e.AllOff()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if controlPath != "" {
				e.consumeControl(controlPath)
			}
			e.Tick()
		}
	}
}
