package flow

import (
	"encoding/json"
	"reflect"
	"testing"
)

func richGraph() Graph {
	return Graph{
		Nodes: []Node{
			{ID: "t", Type: "trigger", DeviceID: "d", PointID: "p", Op: ">", Value: 10, X: 10, Y: 20},
			{ID: "sw", Type: "switch", Property: "value", Mode: "all", Rules: []SwitchRule{{Op: ">", Value: 50.0}, {Op: "contains", Value: "x"}, {Op: "else"}}},
			{ID: "ch", Type: "change", Changes: []Change{{Action: "set", Property: "vars.level", Value: "high"}, {Action: "mul", Property: "value", Value: 2.0}, {Action: "move", Property: "vars.a", To: "vars.b"}, {Action: "delete", Property: "vars.b"}}},
			{ID: "dl", Type: "delay", Seconds: 30},
			{ID: "db", Type: "debug", Name: "look"},
			{ID: "n", Type: "notify", ChannelID: "c1", Message: "hi {value}"},
		},
		Edges: []Edge{{From: "t", Port: "0", To: "sw"}, {From: "sw", Port: "0", To: "ch"}, {From: "sw", Port: "1", To: "dl"}, {From: "sw", Port: "2", To: "db"}, {From: "ch", Port: "0", To: "n"}, {From: "dl", Port: "0", To: "n"}},
	}
}

func TestNodeREDRoundTrip(t *testing.T) {
	g := richGraph()
	if err := Validate(Definition{Graph: &g}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(ToNodeRED(g, "demo")) // as it travels over the wire
	var wire []map[string]any
	json.Unmarshal(raw, &wire)
	back, un, err := FromNodeRED(wire)
	if err != nil || len(un) != 0 {
		t.Fatal(err, un)
	}
	// edge order may differ; compare as sets and compare nodes exactly
	if !reflect.DeepEqual(back.Nodes, g.Nodes) {
		t.Fatalf("nodes differ:\n%+v\n%+v", back.Nodes, g.Nodes)
	}
	set := func(es []Edge) map[Edge]bool {
		m := map[Edge]bool{}
		for _, e := range es {
			m[e] = true
		}
		return m
	}
	if !reflect.DeepEqual(set(back.Edges), set(g.Edges)) {
		t.Fatalf("edges differ: %+v", back.Edges)
	}
	// behaviour survives the round trip
	a := Definition{Graph: &g}.Exec(60, "d", "p", ExecOptions{})
	b := Definition{Graph: &back}.Exec(60, "d", "p", ExecOptions{})
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("behaviour differs: %+v vs %+v", a, b)
	}
}

func TestNodeREDRefusesUnsupportedNodes(t *testing.T) {
	_, un, err := FromNodeRED([]map[string]any{
		{"id": "a", "type": "inject"},
		{"id": "b", "type": "http request"},
		{"id": "c", "type": "mqtt in"},
		{"id": "d", "type": "delay", "pauseType": "rate"},
		{"id": "e", "type": "switch", "property": "payload.deep.path", "propertyType": "msg", "rules": []any{}},
		{"id": "f", "type": "change", "rules": []any{map[string]any{"t": "set", "p": "payload", "pt": "msg", "to": "x", "tot": "jsonata"}}},
	})
	if err == nil || len(un) < 6 {
		t.Fatalf("expected 6+ unsupported, got %v %v", un, err)
	}
}
