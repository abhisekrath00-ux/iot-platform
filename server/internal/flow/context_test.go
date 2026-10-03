package flow

import (
	"strings"
	"testing"
)

type memCtx struct{ m map[string]float64 }

func (c *memCtx) Get(s, k string) (float64, bool, error) { v, ok := c.m[s+"|"+k]; return v, ok, nil }
func (c *memCtx) Set(s, k string, v float64) error       { c.m[s+"|"+k] = v; return nil }
func (c *memCtx) Incr(s, k string, d float64) (float64, error) {
	c.m[s+"|"+k] += d
	return c.m[s+"|"+k], nil
}

// counts readings and notifies on the third one: state survives between runs.
func countFlow() Definition {
	g := Graph{
		Nodes: []Node{
			trig(),
			{ID: "c", Type: "context", Mode: "incr", Scope: "flow", Key: "hits", Value: 1, Target: "n"},
			{ID: "sw", Type: "switch", Property: "vars.n", Rules: []SwitchRule{{Op: ">=", Value: 3.0}}},
			{ID: "ok", Type: "notify", ChannelID: "ch", Message: "seen {vars.n} readings"},
		},
		Edges: []Edge{{From: "t", To: "c"}, {From: "c", To: "sw"}, {From: "sw", Port: "0", To: "ok"}},
	}
	return Definition{Graph: &g}
}

func TestContextIncrAcrossRuns(t *testing.T) {
	d := countFlow()
	if err := Validate(d); err != nil {
		t.Fatal(err)
	}
	store := &memCtx{m: map[string]float64{}}
	opt := ExecOptions{Context: store, LimitKey: "f1"}
	for i := 1; i <= 3; i++ {
		r := d.Exec(50, "dev", "temp", opt)
		if got := len(r.Actions) > 0; got != (i == 3) {
			t.Fatalf("run %d notified=%v", i, got)
		}
		if i == 3 && !strings.Contains(r.Actions[0].Message, "seen 3 readings") {
			t.Fatalf("message %q", r.Actions[0].Message)
		}
	}
	// another flow has its own flow-scope counter
	if r := d.Exec(50, "dev", "temp", ExecOptions{Context: store, LimitKey: "f2"}); len(r.Actions) != 0 {
		t.Fatal("flow scope leaked between flows")
	}
}

func TestContextGetMissingTakesPort1AndDryRunPassesThrough(t *testing.T) {
	g := Graph{
		Nodes: []Node{
			trig(),
			{ID: "c", Type: "context", Mode: "get", Scope: "global", Key: "setpoint", Target: "sp"},
			{ID: "found", Type: "notify", ChannelID: "a", Message: "sp {vars.sp}"},
			{ID: "none", Type: "notify", ChannelID: "b", Message: "unset"},
		},
		Edges: []Edge{{From: "t", To: "c"}, {From: "c", Port: "0", To: "found"}, {From: "c", Port: "1", To: "none"}},
	}
	d := Definition{Graph: &g}
	if err := Validate(d); err != nil {
		t.Fatal(err)
	}
	store := &memCtx{m: map[string]float64{}}
	if r := d.Exec(50, "dev", "temp", ExecOptions{Context: store}); len(r.Actions) != 1 || r.Actions[0].Message != "unset" {
		t.Fatalf("missing key: %+v", r.Actions)
	}
	store.m["global|setpoint"] = 42
	if r := d.Exec(50, "dev", "temp", ExecOptions{Context: store}); len(r.Actions) != 1 || r.Actions[0].Message != "sp 42" {
		t.Fatalf("found: %+v", r.Actions)
	}
	// dry run: no store, the node passes through on port 0 and the message shows the variable unset
	if r := d.Exec(50, "dev", "temp", ExecOptions{}); len(r.Debug) == 0 || !strings.Contains(r.Debug[0].Message, "dry run") {
		t.Fatalf("dry run: %+v", r.Debug)
	}
}

func TestContextValidation(t *testing.T) {
	bad := []Node{
		{ID: "c", Type: "context", Mode: "nope", Scope: "flow", Key: "k"},
		{ID: "c", Type: "context", Mode: "set", Scope: "tenant", Key: "k"},
		{ID: "c", Type: "context", Mode: "set", Scope: "flow", Key: "bad key!"},
		{ID: "c", Type: "context", Mode: "get", Scope: "flow", Key: "k"}, // no target
	}
	for i, n := range bad {
		n := n
		if err := validateNode(&n); err == nil {
			t.Fatalf("case %d accepted", i)
		}
	}
}
