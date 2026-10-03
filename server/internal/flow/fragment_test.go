package flow

import "testing"

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
