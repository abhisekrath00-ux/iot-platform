package flow

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeControl struct {
	calls  int
	target string
	value  float64
	err    error
	note   string
}

func (f *fakeControl) RequestControl(_ context.Context, target string, v float64) (string, string, error) {
	f.calls++
	f.target, f.value = target, v
	return "req-1", f.note, f.err
}

func controlFlow(useValue bool) Definition {
	g := Graph{
		Nodes: []Node{
			trig(),
			{ID: "c", Type: "control", TargetID: "tg-1", Value: 1, UseValue: useValue},
			{ID: "ok", Type: "notify", ChannelID: "ok", Message: "asked"},
			{ID: "no", Type: "notify", ChannelID: "no", Message: "refused"},
		},
		Edges: []Edge{{From: "t", To: "c"}, {From: "c", Port: "0", To: "ok"}, {From: "c", Port: "1", To: "no"}},
	}
	return Definition{Graph: &g}
}

func TestControlNodeRaisesRequestAndRoutes(t *testing.T) {
	d := controlFlow(false)
	if err := Validate(d); err != nil {
		t.Fatal(err)
	}
	f := &fakeControl{}
	r := d.Exec(20, "d", "p", ExecOptions{Control: f})
	if f.calls != 1 || f.target != "tg-1" || f.value != 1 {
		t.Fatalf("calls=%d target=%q value=%v", f.calls, f.target, f.value)
	}
	if len(r.Actions) != 1 || r.Actions[0].ChannelID != "ok" {
		t.Fatalf("%+v %+v", r.Actions, r.Debug)
	}
	if !strings.Contains(r.Debug[0].Message, "waiting for approval") {
		t.Fatalf("debug must say it waits for approval: %+v", r.Debug)
	}
}

func TestControlNodeUsesMessageValueOnlyWhenAsked(t *testing.T) {
	f := &fakeControl{}
	controlFlow(true).Exec(20, "d", "p", ExecOptions{Control: f})
	if f.value != 20 {
		t.Fatalf("use_value: got %v", f.value)
	}
}

func TestControlNodeRefusalAndDryRunRaiseNothing(t *testing.T) {
	f := &fakeControl{err: errors.New("value 99 is outside 0..10")}
	r := controlFlow(false).Exec(20, "d", "p", ExecOptions{Control: f})
	if len(r.Actions) != 1 || r.Actions[0].ChannelID != "no" || !strings.Contains(r.Debug[0].Message, "refused") {
		t.Fatalf("%+v %+v", r.Actions, r.Debug)
	}
	// no requester (feature off, dry run, simulator): the refusal route, and nothing was asked for
	r = controlFlow(false).Exec(20, "d", "p", ExecOptions{})
	if len(r.Actions) != 1 || r.Actions[0].ChannelID != "no" || !strings.Contains(r.Debug[0].Message, "dry run") {
		t.Fatalf("%+v %+v", r.Actions, r.Debug)
	}
}

func TestControlNodeAtMostOncePerRun(t *testing.T) {
	g := Graph{
		Nodes: []Node{
			trig(),
			{ID: "c1", Type: "control", TargetID: "a"},
			{ID: "c2", Type: "control", TargetID: "b"},
			{ID: "ok", Type: "notify", ChannelID: "ok", Message: "x"},
			{ID: "no", Type: "notify", ChannelID: "no", Message: "y"},
		},
		Edges: []Edge{{From: "t", To: "c1"}, {From: "t", To: "c2"}, {From: "c1", To: "ok"}, {From: "c2", Port: "0", To: "ok"}, {From: "c2", Port: "1", To: "no"}},
	}
	f := &fakeControl{}
	d := Definition{Graph: &g}
	if err := Validate(d); err != nil {
		t.Fatal(err)
	}
	d.Exec(20, "d", "p", ExecOptions{Control: f})
	if f.calls != 1 {
		t.Fatalf("a run may raise one control request, got %d", f.calls)
	}
}

func TestControlNodeValidation(t *testing.T) {
	d := controlFlow(false)
	d.Graph.Nodes[1].TargetID = ""
	if Validate(d) == nil {
		t.Fatal("target_id is required")
	}
	d = controlFlow(false)
	d.Graph.Edges = append(d.Graph.Edges, Edge{From: "c", Port: "2", To: "ok"})
	if Validate(d) == nil {
		t.Fatal("control has two output ports only")
	}
	if !controlFlow(false).HasControlNodes() || trigOnly().HasControlNodes() {
		t.Fatal("HasControlNodes wrong")
	}
}

func trigOnly() Definition {
	g := Graph{Nodes: []Node{trig(), {ID: "n", Type: "notify", ChannelID: "x", Message: "m"}}, Edges: []Edge{{From: "t", To: "n"}}}
	return Definition{Graph: &g}
}
