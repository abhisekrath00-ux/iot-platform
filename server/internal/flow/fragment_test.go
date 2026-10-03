package flow

import (
	"fmt"
	"testing"
)

func frag() Graph {
	return Graph{
		Nodes: []Node{
			{ID: "r", Type: "range", InMin: 0, InMax: 100, OutMin: 0, OutMax: 1, Clamp: true, X: 10, Y: 20},
			{ID: "c", Type: "condition", Op: ">", Value: 0.5},
		},
		Edges: []Edge{{From: "r", To: "c"}},
	}
}

func TestFragmentValidateAndInstantiate(t *testing.T) {
	entry, err := ValidateFragment(frag())
	if err != nil || entry != "r" {
		t.Fatalf("%v %v", entry, err)
	}
	nodes, edges, e, exits, err := InstantiateFragment(frag(), "hi", 100, 50)
	if err != nil || e != "hi_r" || len(nodes) != 2 || edges[0].From != "hi_r" || edges[0].To != "hi_c" {
		t.Fatalf("%v %v %v", nodes, edges, err)
	}
	if len(exits) != 1 || exits[0] != "hi_c" || nodes[0].X != 110 || nodes[0].Y != 70 {
		t.Fatalf("exits %v node %+v", exits, nodes[0])
	}
	// two copies in one flow must not clash
	n2, _, _, _, _ := InstantiateFragment(frag(), "lo", 0, 0)
	if n2[0].ID == nodes[0].ID {
		t.Fatal("ids clash")
	}
}

func TestFragmentRejects(t *testing.T) {
	cases := map[string]Graph{
		"start node": {Nodes: []Node{{ID: "t", Type: "trigger", DeviceID: "d", PointID: "p", Op: ">"}}},
		"two entries": {Nodes: []Node{
			{ID: "a", Type: "condition", Op: ">"}, {ID: "b", Type: "condition", Op: ">"}}},
		"cycle": {Nodes: []Node{{ID: "a", Type: "condition", Op: ">"}, {ID: "b", Type: "condition", Op: ">"}, {ID: "c", Type: "condition", Op: ">"}},
			Edges: []Edge{{From: "a", To: "b"}, {From: "b", To: "c"}, {From: "c", To: "b"}}},
		"bad port": {Nodes: []Node{{ID: "a", Type: "condition", Op: ">"}, {ID: "b", Type: "condition", Op: ">"}},
			Edges: []Edge{{From: "a", Port: "3", To: "b"}}},
		"outside edge": {Nodes: []Node{{ID: "a", Type: "condition", Op: ">"}}, Edges: []Edge{{From: "a", To: "zzz"}}},
		"slash in id":  {Nodes: []Node{{ID: "a/b", Type: "condition", Op: ">"}}},
		"empty":        {},
	}
	for name, g := range cases {
		if _, err := ValidateFragment(g); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, _, _, _, err := InstantiateFragment(frag(), "bad prefix", 0, 0); err == nil {
		t.Error("bad prefix accepted")
	}
}

func TestExpandSubflowsWiring(t *testing.T) {
	two := Graph{Nodes: []Node{{ID: "a", Type: "condition", Op: ">"}, {ID: "b", Type: "condition", Op: "<"}, {ID: "c", Type: "condition", Op: ">"}},
		Edges: []Edge{{From: "a", To: "b"}, {From: "a", To: "c"}}} // two exits: b and c
	res := func(id string, v int) (Graph, int, error) {
		if id != "f" {
			return Graph{}, 0, fmt.Errorf("fragment not found")
		}
		if v == 0 {
			v = 3
		}
		return two, v, nil
	}
	src := Graph{Nodes: []Node{{ID: "t", Type: "trigger", DeviceID: "d", PointID: "p", Op: ">"}, {ID: "s", Type: "subflow", FragmentID: "f"}, {ID: "n", Type: "debug"}},
		Edges: []Edge{{From: "t", To: "s"}, {From: "s", To: "n"}}}
	exp, pin, err := ExpandSubflows(src, res)
	if err != nil {
		t.Fatal(err)
	}
	if pin.Nodes[1].FragmentVersion != 3 || len(exp.Nodes) != 5 {
		t.Fatalf("pin %d nodes %d", pin.Nodes[1].FragmentVersion, len(exp.Nodes))
	}
	want := map[string]bool{"t>s_a": true, "s_a>s_b": true, "s_a>s_c": true, "s_b>n": true, "s_c>n": true}
	for _, e := range exp.Edges {
		delete(want, e.From+">"+e.To)
	}
	if len(want) != 0 {
		t.Fatalf("missing edges %v in %v", want, exp.Edges)
	}
	if src.Nodes[1].FragmentVersion != 0 {
		t.Fatal("source mutated")
	}
	bad := Graph{Nodes: []Node{{ID: "s", Type: "subflow", FragmentID: "nope"}}}
	if _, _, err := ExpandSubflows(bad, res); err == nil {
		t.Fatal("unknown fragment accepted")
	}
	clash := Graph{Nodes: []Node{{ID: "s", Type: "subflow", FragmentID: "f"}, {ID: "s_a", Type: "debug"}}}
	if _, _, err := ExpandSubflows(clash, res); err == nil {
		t.Fatal("id clash accepted")
	}
}
