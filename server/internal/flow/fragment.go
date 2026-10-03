package flow

import (
	"fmt"
	"strconv"
	"strings"
)

// A fragment is a reusable piece of a flow: nodes and edges with no start node and exactly one entry
// (the node nothing points at). It is copied into a flow when inserted; later edits to the fragment do
// not change flows that already use it.
const maxFragmentNodes = 40

// ValidateFragment checks a fragment and returns its entry node id.
func ValidateFragment(g Graph) (string, error) {
	if len(g.Nodes) == 0 || len(g.Nodes) > maxFragmentNodes {
		return "", fmt.Errorf("a fragment needs 1-%d nodes", maxFragmentNodes)
	}
	if len(g.Edges) > maxEdges {
		return "", fmt.Errorf("at most %d edges", maxEdges)
	}
	idx := map[string]*Node{}
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.ID == "" || len(n.ID) > 64 || strings.ContainsAny(n.ID, "<>\x00 /") {
			return "", fmt.Errorf("node %d: bad id", i)
		}
		if _, dup := idx[n.ID]; dup {
			return "", fmt.Errorf("duplicate node id %q", n.ID)
		}
		if isStart(n.Type) {
			return "", fmt.Errorf("a fragment cannot contain a start node (%s)", n.ID)
		}
		if len(n.Name) > 64 || strings.ContainsAny(n.Name, "<>\x00") {
			return "", fmt.Errorf("node %s: bad name", n.ID)
		}
		if err := validateNode(n); err != nil {
			return "", fmt.Errorf("node %s: %w", n.ID, err)
		}
		idx[n.ID] = n
	}
	indeg := map[string]int{}
	adj := map[string][]string{}
	for _, e := range g.Edges {
		from, to := idx[e.From], idx[e.To]
		if from == nil || to == nil {
			return "", fmt.Errorf("edge references a node outside the fragment")
		}
		if from.Type == "notify" || from.Type == "debug" {
			return "", fmt.Errorf("node %s is terminal and cannot have outputs", from.ID)
		}
		p := e.Port
		if p == "" {
			p = "0"
		}
		max := 1
		switch from.Type {
		case "switch":
			max = len(from.Rules)
		case "http", "control", "context":
			max = 2
		}
		if pn, err := strconv.Atoi(p); err != nil || pn < 0 || pn >= max {
			return "", fmt.Errorf("node %s has no output port %q", from.ID, p)
		}
		indeg[e.To]++
		adj[e.From] = append(adj[e.From], e.To)
	}
	entry := ""
	for _, n := range g.Nodes {
		if indeg[n.ID] == 0 {
			if entry != "" {
				return "", fmt.Errorf("a fragment needs exactly one entry node; both %s and %s have no input", entry, n.ID)
			}
			entry = n.ID
		}
	}
	if entry == "" {
		return "", fmt.Errorf("a fragment needs an entry node (every node has an input, so there is a cycle)")
	}
	deg := map[string]int{}
	for k, v := range indeg {
		deg[k] = v
	}
	q, seen := []string{entry}, 0
	for len(q) > 0 {
		x := q[0]
		q = q[1:]
		seen++
		for _, y := range adj[x] {
			if deg[y]--; deg[y] == 0 {
				q = append(q, y)
			}
		}
	}
	if seen != len(g.Nodes) {
		return "", fmt.Errorf("fragment has a cycle or a node not reachable from its entry")
	}
	return entry, nil
}

// InstantiateFragment copies a fragment with every id prefixed, positions shifted by (dx, dy). It returns
// the entry node id (prefixed) and the exits: nodes with no outgoing edge that can still pass a message on.
func InstantiateFragment(g Graph, prefix string, dx, dy float64) (nodes []Node, edges []Edge, entry string, exits []string, err error) {
	e, err := ValidateFragment(g)
	if err != nil {
		return nil, nil, "", nil, err
	}
	if prefix == "" || len(prefix) > 20 || strings.ContainsAny(prefix, "<>\x00 /") {
		return nil, nil, "", nil, fmt.Errorf("bad prefix")
	}
	hasOut := map[string]bool{}
	for _, ed := range g.Edges {
		hasOut[ed.From] = true
	}
	for _, n := range g.Nodes {
		c := n
		c.ID = prefix + "_" + n.ID
		c.X, c.Y = n.X+dx, n.Y+dy
		nodes = append(nodes, c)
		if !hasOut[n.ID] && n.Type != "notify" && n.Type != "debug" {
			exits = append(exits, c.ID)
		}
	}
	for _, ed := range g.Edges {
		ed.From, ed.To = prefix+"_"+ed.From, prefix+"_"+ed.To
		edges = append(edges, ed)
	}
	return nodes, edges, prefix + "_" + e, exits, nil
}
