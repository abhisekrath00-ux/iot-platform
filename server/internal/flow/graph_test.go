package flow

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func trig() Node {
	return Node{ID: "t", Type: "trigger", DeviceID: "d", PointID: "p", Op: ">", Value: 10}
}

func TestLegacyAndConvertedGraphAgree(t *testing.T) {
	d := Definition{
		Trigger: Trigger{DeviceID: "d", PointID: "p", Op: ">", Value: 10},
		Steps: []Step{
			{Type: "condition", Op: "<", Value: 100},
			{Type: "delay", Seconds: 30},
			{Type: "notify", ChannelID: "c1", Message: "hot {value}"},
		},
	}
	g := ToGraph(d)
	gd := Definition{Graph: &g}
	if err := Validate(gd); err != nil {
		t.Fatal(err)
	}
	for _, v := range []float64{5, 11, 99.5, 100, 500} {
		a := d.Exec(v, "d", "p", ExecOptions{})
		b := gd.Exec(v, "d", "p", ExecOptions{})
		if a.Matched != b.Matched && len(a.Actions) > 0 {
			t.Fatalf("v=%v matched differs", v)
		}
		if !reflect.DeepEqual(a.Actions, b.Actions) {
			t.Fatalf("v=%v legacy %+v graph %+v", v, a.Actions, b.Actions)
		}
	}
}

func TestSwitchRoutesAndChangeRewrites(t *testing.T) {
	g := Graph{
		Nodes: []Node{
			trig(),
			{ID: "sw", Type: "switch", Property: "value", Rules: []SwitchRule{{Op: ">", Value: 50.0}, {Op: "else"}}},
			{ID: "ch", Type: "change", Changes: []Change{{Action: "set", Property: "vars.level", Value: "critical"}, {Action: "mul", Property: "value", Value: 2.0}}},
			{ID: "nCrit", Type: "notify", ChannelID: "crit", Message: "{vars.level} {value}"},
			{ID: "nWarn", Type: "notify", ChannelID: "warn", Message: "warn {value}"},
		},
		Edges: []Edge{{From: "t", To: "sw"}, {From: "sw", Port: "0", To: "ch"}, {From: "sw", Port: "1", To: "nWarn"}, {From: "ch", To: "nCrit"}},
	}
	d := Definition{Graph: &g}
	if err := Validate(d); err != nil {
		t.Fatal(err)
	}
	r := d.Exec(60, "d", "p", ExecOptions{})
	if len(r.Actions) != 1 || r.Actions[0].ChannelID != "crit" || r.Actions[0].Message != "critical 120" {
		t.Fatalf("%+v", r.Actions)
	}
	r = d.Exec(20, "d", "p", ExecOptions{})
	if len(r.Actions) != 1 || r.Actions[0].ChannelID != "warn" {
		t.Fatalf("%+v", r.Actions)
	}
	if r := d.Exec(5, "d", "p", ExecOptions{}); r.Matched || len(r.Actions) != 0 {
		t.Fatal("trigger should not match")
	}
}

func TestSwitchAllModeFansOutAndDelayAccumulates(t *testing.T) {
	g := Graph{
		Nodes: []Node{
			trig(),
			{ID: "sw", Type: "switch", Mode: "all", Property: "value", Rules: []SwitchRule{{Op: ">", Value: 1.0}, {Op: "<", Value: 1000.0}}},
			{ID: "dl", Type: "delay", Seconds: 60},
			{ID: "a", Type: "notify", ChannelID: "a"},
			{ID: "b", Type: "notify", ChannelID: "b"},
		},
		Edges: []Edge{{From: "t", To: "sw"}, {From: "sw", Port: "0", To: "dl"}, {From: "dl", To: "a"}, {From: "sw", Port: "1", To: "b"}},
	}
	d := Definition{Graph: &g}
	if err := Validate(d); err != nil {
		t.Fatal(err)
	}
	r := d.Exec(20, "d", "p", ExecOptions{})
	if len(r.Actions) != 2 {
		t.Fatalf("%+v", r.Actions)
	}
	var da, db time.Duration
	for _, a := range r.Actions {
		if a.ChannelID == "a" {
			da = a.Delay
		} else {
			db = a.Delay
		}
	}
	if da != 60*time.Second || db != 0 {
		t.Fatalf("delays %v %v", da, db)
	}
}

func TestDebugNodeRecordsAndFailedChangeStopsPath(t *testing.T) {
	g := Graph{
		Nodes: []Node{
			trig(),
			{ID: "dbg", Type: "debug", Message: "seen {device_id}={value}"},
			{ID: "bad", Type: "change", Changes: []Change{{Action: "set", Property: "value", Value: 1.0}}},
			{ID: "n", Type: "notify", ChannelID: "c"},
		},
		Edges: []Edge{{From: "t", To: "dbg"}, {From: "t", To: "n"}, {From: "t", To: "bad"}, {From: "bad", To: "n"}},
	}
	d := Definition{Graph: &g}
	if err := Validate(d); err != nil {
		t.Fatal(err)
	}
	// an unreachable node must be rejected
	if err := Validate(Definition{Graph: &Graph{Nodes: g.Nodes, Edges: g.Edges[:0]}}); err == nil {
		t.Fatal("expected unreachable error")
	}
	r := d.Exec(20, "d", "p", ExecOptions{})
	if len(r.Debug) != 1 || r.Debug[0].Message != "seen d=20" || len(r.Actions) != 2 {
		t.Fatalf("%+v", r)
	}
}

func TestValidateRejectsBadGraphs(t *testing.T) {
	n := func(id, typ string) Node { return Node{ID: id, Type: typ, ChannelID: "c", Seconds: 5, Op: ">"} }
	cases := map[string]Graph{
		"cycle": {Nodes: []Node{trig(), n("a", "delay"), n("b", "delay"), n("n", "notify")},
			Edges: []Edge{{From: "t", To: "a"}, {From: "a", To: "b"}, {From: "b", To: "a"}, {From: "b", To: "n"}}},
		"orphan":    {Nodes: []Node{trig(), n("n", "notify"), n("o", "delay")}, Edges: []Edge{{From: "t", To: "n"}}},
		"no notify": {Nodes: []Node{trig(), n("d", "delay")}, Edges: []Edge{{From: "t", To: "d"}}},
		"two trigs": {Nodes: []Node{trig(), {ID: "t2", Type: "trigger", DeviceID: "d", PointID: "p", Op: ">"}, n("n", "notify")}, Edges: []Edge{{From: "t", To: "n"}}},
		"terminal out": {Nodes: []Node{trig(), n("n", "notify"), n("m", "notify")},
			Edges: []Edge{{From: "t", To: "n"}, {From: "n", To: "m"}}},
		"bad port":  {Nodes: []Node{trig(), n("n", "notify")}, Edges: []Edge{{From: "t", Port: "3", To: "n"}}},
		"dup id":    {Nodes: []Node{trig(), n("n", "notify"), n("n", "notify")}, Edges: []Edge{{From: "t", To: "n"}}},
		"unknown":   {Nodes: []Node{trig(), n("x", "teleport")}, Edges: []Edge{{From: "t", To: "x"}}},
		"into trig": {Nodes: []Node{trig(), n("n", "notify")}, Edges: []Edge{{From: "t", To: "n"}, {From: "n", To: "t"}}},
		"empty fn":  {Nodes: []Node{trig(), {ID: "f", Type: "function"}, n("n", "notify")}, Edges: []Edge{{From: "t", To: "f"}, {From: "f", To: "n"}}},
		"bad prop":  {Nodes: []Node{trig(), {ID: "c", Type: "change", Changes: []Change{{Action: "set", Property: "vars.a b", Value: "x"}}}, n("n", "notify")}, Edges: []Edge{{From: "t", To: "c"}, {From: "c", To: "n"}}},
	}
	for name, g := range cases {
		g := g
		if err := Validate(Definition{Graph: &g}); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if err := Validate(Definition{Graph: &Graph{Nodes: []Node{trig(), n("n", "notify")}, Edges: []Edge{{From: "t", To: "n"}}}, Steps: []Step{{Type: "delay"}}}); err == nil {
		t.Error("graph plus steps must be rejected")
	}
}

func TestVisitLimitBoundsFanOut(t *testing.T) {
	// a ladder of "all" switches doubles the messages at every rung
	g := Graph{Nodes: []Node{trig()}}
	prev := "t"
	for i := 0; i < 20; i++ {
		id := "s" + string(rune('a'+i))
		g.Nodes = append(g.Nodes, Node{ID: id, Type: "switch", Mode: "all", Property: "value", Rules: []SwitchRule{{Op: ">", Value: 0.0}, {Op: ">", Value: 0.0}}})
		g.Edges = append(g.Edges, Edge{From: prev, To: id}, Edge{From: prev, To: id})
		prev = id
	}
	g.Nodes = append(g.Nodes, Node{ID: "n", Type: "notify", ChannelID: "c"})
	g.Edges = append(g.Edges, Edge{From: prev, To: "n"})
	d := Definition{Graph: &g}
	r := d.Exec(50, "d", "p", ExecOptions{})
	if len(r.Debug) == 0 || !strings.Contains(r.Debug[len(r.Debug)-1].Message, "limit") {
		t.Fatalf("expected visit-limit entry, got %d debug entries", len(r.Debug))
	}
}

func TestFunctionNodeDisabledWithoutRunner(t *testing.T) {
	g := Graph{Nodes: []Node{trig(), {ID: "f", Type: "function", Code: "return msg"}, {ID: "n", Type: "notify", ChannelID: "c"}},
		Edges: []Edge{{From: "t", To: "f"}, {From: "f", To: "n"}}}
	d := Definition{Graph: &g}
	r := d.Exec(50, "d", "p", ExecOptions{})
	if len(r.Actions) != 0 || len(r.Debug) != 1 || !strings.Contains(r.Debug[0].Message, "disabled") {
		t.Fatalf("%+v", r)
	}
}

func TestChannelSlotsAndTrigForGraph(t *testing.T) {
	g := Graph{Nodes: []Node{trig(), {ID: "n", Type: "notify", ChannelID: "c"}}, Edges: []Edge{{From: "t", To: "n"}}}
	d := Definition{Graph: &g}
	if d.Trig().DeviceID != "d" {
		t.Fatal("trig")
	}
	s := d.ChannelSlots()
	if len(s) != 1 || *s[1] != "c" {
		t.Fatal("slots")
	}
	*s[1] = "x"
	if g.Nodes[1].ChannelID != "x" {
		t.Fatal("slot must alias the node")
	}
}
