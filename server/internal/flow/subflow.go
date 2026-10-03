package flow

import (
	"fmt"
	"strings"
)

// Live (by-reference) subflows.
//
// A flow's draft may contain "subflow" nodes that point at a saved fragment (fragment_id) at a pinned
// fragment_version. Before a definition is stored or validated, the server expands each subflow node into the
// fragment's nodes (ids prefixed with the subflow node's id). The engine only ever runs the expanded graph,
// so there is no new runtime behaviour. The reference graph is kept in Definition.Source for the editor.
//
// Safety rules: a fragment cannot contain a subflow node (depth one, so no cycles); versions are pinned, so
// editing a fragment never changes a published flow by itself. A newer fragment version reaches a flow only
// through a new draft and a normal publish.
const maxSubflowNodes = 10

// FragmentResolver returns the graph of a fragment at a version (0 means the current one) and the version
// it resolved to.
type FragmentResolver func(id string, version int) (Graph, int, error)

// HasSubflows reports whether the graph contains a subflow node.
func HasSubflows(g *Graph) bool {
	if g == nil {
		return false
	}
	for _, n := range g.Nodes {
		if n.Type == "subflow" {
			return true
		}
	}
	return false
}

// ExpandSubflows returns the expanded graph and a copy of the source with every subflow pin resolved.
func ExpandSubflows(src Graph, resolve FragmentResolver) (expanded Graph, pinned Graph, err error) {
	pinned = Graph{Nodes: append([]Node(nil), src.Nodes...), Edges: append([]Edge(nil), src.Edges...)}
	type sub struct {
		entry string
		exits []string
	}
	subs := map[string]sub{}
	ids := map[string]bool{}
	for _, n := range src.Nodes {
		ids[n.ID] = true
	}
	count := 0
	for i, n := range pinned.Nodes {
		if n.Type != "subflow" {
			expanded.Nodes = append(expanded.Nodes, n)
			continue
		}
		count++
		if count > maxSubflowNodes {
			return Graph{}, Graph{}, fmt.Errorf("at most %d subflow nodes per flow", maxSubflowNodes)
		}
		if n.ID == "" || len(n.ID) > 20 || strings.ContainsAny(n.ID, "<>\x00 /") {
			return Graph{}, Graph{}, fmt.Errorf("subflow node id must be 1-20 characters with no spaces")
		}
		if n.FragmentID == "" {
			return Graph{}, Graph{}, fmt.Errorf("subflow %s: fragment_id required", n.ID)
		}
		g, ver, err := resolve(n.FragmentID, n.FragmentVersion)
		if err != nil {
			return Graph{}, Graph{}, fmt.Errorf("subflow %s: %w", n.ID, err)
		}
		for _, fn := range g.Nodes {
			if fn.Type == "subflow" {
				return Graph{}, Graph{}, fmt.Errorf("subflow %s: fragments cannot contain subflows", n.ID)
			}
		}
		pinned.Nodes[i].FragmentVersion = ver
		nodes, edges, entry, exits, err := InstantiateFragment(g, n.ID, n.X, n.Y)
		if err != nil {
			return Graph{}, Graph{}, fmt.Errorf("subflow %s: %w", n.ID, err)
		}
		for _, fn := range nodes {
			if ids[fn.ID] {
				return Graph{}, Graph{}, fmt.Errorf("subflow %s: id %s clashes with another node", n.ID, fn.ID)
			}
			ids[fn.ID] = true
		}
		expanded.Nodes = append(expanded.Nodes, nodes...)
		expanded.Edges = append(expanded.Edges, edges...)
		subs[n.ID] = sub{entry: entry, exits: exits}
	}
	for _, e := range src.Edges {
		from, fromSub := subs[e.From]
		to, toSub := subs[e.To]
		if fromSub && e.Port != "" && e.Port != "0" {
			return Graph{}, Graph{}, fmt.Errorf("subflow %s has one output", e.From)
		}
		if fromSub && len(from.exits) == 0 {
			return Graph{}, Graph{}, fmt.Errorf("subflow %s has no output to connect", e.From)
		}
		tos := []string{e.To}
		if toSub {
			tos = []string{to.entry}
		}
		froms := []string{e.From}
		if fromSub {
			froms = from.exits
		}
		for _, f := range froms {
			for _, t := range tos {
				ne := Edge{From: f, To: t}
				if !fromSub {
					ne.Port = e.Port
				}
				expanded.Edges = append(expanded.Edges, ne)
			}
		}
	}
	return expanded, pinned, nil
}
