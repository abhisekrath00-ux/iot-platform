// Package flow validates and executes tenant-defined automation pipelines:
// one trigger (point threshold) plus ordered steps (condition, delay,
// notify). Execution is deterministic and side effects (notify) are returned
// as instructions so the engine - and tests - control dispatch.
package flow

import (
	"errors"
	"fmt"
	"time"
)

type Trigger struct {
	DeviceID string  `json:"device_id"`
	PointID  string  `json:"point_id"`
	Op       string  `json:"op"` // > < >= <= == !=
	Value    float64 `json:"value"`
}

type Step struct {
	Type      string  `json:"type"`                 // condition|delay|notify
	Op        string  `json:"op,omitempty"`         // condition: same ops as trigger
	Value     float64 `json:"value,omitempty"`      // condition threshold
	Seconds   int     `json:"seconds,omitempty"`    // delay, max 3600
	ChannelID string  `json:"channel_id,omitempty"` // notify target
	Message   string  `json:"message,omitempty"`    // notify text; {value} placeholder
}

type Definition struct {
	Trigger Trigger `json:"trigger"`
	Steps   []Step  `json:"steps"`
	// CooldownSeconds suppresses repeat notifications: after a run notifies,
	// further matching readings are recorded as suppressed until the cooldown
	// passes. 0 = notify on every match (previous behaviour). Max 24h.
	CooldownSeconds int `json:"cooldown_seconds,omitempty"`
	// Latch notifies once when the flow starts matching and stays quiet until
	// a reading (or step condition) stops matching, which re-arms it. Unlike
	// cooldown it has no timer. May be combined with cooldown.
	Latch bool `json:"latch,omitempty"`
}

// LatchHolds reports whether a latched flow should stay quiet: the most recent
// recorded outcome is a notification or a latch suppression, i.e. no
// non-matching reading has re-armed it. An empty lastOutcome (first run) never holds.
func LatchHolds(latch bool, lastOutcome string) bool {
	return latch && (lastOutcome == "notified" || lastOutcome == "suppressed_latch")
}

// InCooldown reports whether a run at now is inside the cooldown that began at
// lastNotified. A zero lastNotified or non-positive cooldown is never in cooldown.
func InCooldown(lastNotified, now time.Time, cooldownSeconds int) bool {
	if cooldownSeconds <= 0 || lastNotified.IsZero() {
		return false
	}
	return now.Sub(lastNotified) < time.Duration(cooldownSeconds)*time.Second
}

var (
	ErrBadTrigger = errors.New("trigger: device_id, point_id and a valid op required")
	ErrNoSteps    = errors.New("at least one step required")
)

func validOp(op string) bool {
	switch op {
	case ">", "<", ">=", "<=", "==", "!=":
		return true
	}
	return false
}

func compare(op string, a, b float64) bool {
	switch op {
	case ">":
		return a > b
	case "<":
		return a < b
	case ">=":
		return a >= b
	case "<=":
		return a <= b
	case "==":
		return a == b
	case "!=":
		return a != b
	}
	return false
}

func Validate(d Definition) error {
	if d.Trigger.DeviceID == "" || d.Trigger.PointID == "" || !validOp(d.Trigger.Op) {
		return ErrBadTrigger
	}
	if len(d.Steps) == 0 {
		return ErrNoSteps
	}
	if d.CooldownSeconds < 0 || d.CooldownSeconds > 86400 {
		return fmt.Errorf("cooldown_seconds must be 0-86400")
	}
	if len(d.Steps) > 20 {
		return fmt.Errorf("at most 20 steps")
	}
	notifies := 0
	for i, st := range d.Steps {
		switch st.Type {
		case "condition":
			if !validOp(st.Op) {
				return fmt.Errorf("step %d: bad op", i)
			}
		case "delay":
			if st.Seconds < 1 || st.Seconds > 3600 {
				return fmt.Errorf("step %d: delay 1-3600 seconds", i)
			}
		case "notify":
			if st.ChannelID == "" {
				return fmt.Errorf("step %d: channel_id required", i)
			}
			if len(st.Message) > 500 {
				return fmt.Errorf("step %d: message too long", i)
			}
			notifies++
		default:
			return fmt.Errorf("step %d: unknown type %q", i, st.Type)
		}
	}
	if notifies == 0 {
		return fmt.Errorf("a flow must notify at least once")
	}
	return nil
}

// Action is one dispatch instruction produced by a run.
type Action struct {
	ChannelID string
	Message   string
}

// Run evaluates a flow against one observed value. Conditions compare against
// the trigger value; delays return the wait so the caller decides to sleep
// (the engine sleeps, tests assert). ok=false means a condition stopped it.
func Run(d Definition, value float64) (actions []Action, wait time.Duration, ok bool) {
	if !compare(d.Trigger.Op, value, d.Trigger.Value) {
		return nil, 0, false
	}
	for _, st := range d.Steps {
		switch st.Type {
		case "condition":
			if !compare(st.Op, value, st.Value) {
				return nil, wait, false
			}
		case "delay":
			wait += time.Duration(st.Seconds) * time.Second
		case "notify":
			msg := st.Message
			if msg == "" {
				msg = "flow triggered"
			}
			out := ""
			// replace {value} without exposing other text to formatting
			for {
				i := indexOf(msg, "{value}")
				if i < 0 {
					out += msg
					break
				}
				out += msg[:i] + fmt.Sprintf("%g", value)
				msg = msg[i+7:]
			}
			actions = append(actions, Action{ChannelID: st.ChannelID, Message: out})
		}
	}
	return actions, wait, true
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// Reading is one historical telemetry point for simulation.
type Reading struct {
	ObservedAt time.Time
	Value      float64
}

// SimEvent is one simulated firing: what the flow would have sent, and when.
type SimEvent struct {
	ObservedAt   time.Time
	Value        float64
	Messages     []string
	DelaySeconds int
}

// Simulate replays recorded readings through a definition and reports every
// firing with the exact messages it would have produced. Pure: no dispatch,
// no recording - the caller owns side effects (there are none for simulate).
func Simulate(d Definition, readings []Reading) []SimEvent {
	var out []SimEvent
	for _, r := range readings {
		acts, wait, ok := Run(d, r.Value)
		if !ok || len(acts) == 0 {
			continue
		}
		msgs := make([]string, 0, len(acts))
		for _, a := range acts {
			msgs = append(msgs, a.ChannelID+": "+a.Message)
		}
		out = append(out, SimEvent{ObservedAt: r.ObservedAt, Value: r.Value, Messages: msgs, DelaySeconds: int(wait / time.Second)})
	}
	return out
}
