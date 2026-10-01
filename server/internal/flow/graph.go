package flow

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Graph is the node-graph form of a flow (Node-RED style): typed nodes joined
// by edges. A definition holds either the legacy linear Steps or a Graph;
// legacy flows keep working and ToGraph converts them for the visual editor.

const (
	maxNodes      = 50
	maxEdges      = 150
	maxExecNodes  = 500 // visits per run: bounds fan-out
	maxPathDelay  = 3600
	maxFuncCode   = 4000
	maxVars       = 32
	maxStringSize = 256
)

type Node struct {
	ID   string  `json:"id"`
	Type string  `json:"type"` // trigger|switch|change|condition|delay|notify|debug|function
	Name string  `json:"name,omitempty"`
	X    float64 `json:"x,omitempty"` // editor position only
	Y    float64 `json:"y,omitempty"`

	// trigger and condition
	DeviceID string  `json:"device_id,omitempty"`
	PointID  string  `json:"point_id,omitempty"`
	Op       string  `json:"op,omitempty"`
	Value    float64 `json:"value,omitempty"`
	// switch
	Property string       `json:"property,omitempty"`
	Rules    []SwitchRule `json:"rules,omitempty"`
	Mode     string       `json:"mode,omitempty"` // first (default) | all
	// change
	Changes []Change `json:"changes,omitempty"`
	// delay
	Seconds int `json:"seconds,omitempty"`
	// notify and debug
	ChannelID string `json:"channel_id,omitempty"`
	Message   string `json:"message,omitempty"`
	// function (sandboxed JavaScript; admin-only and off by default per tenant)
	Code string `json:"code,omitempty"`
}

type SwitchRule struct {
	Op    string `json:"op"` // == != > < >= <= contains else
	Value any    `json:"value,omitempty"`
}

type Change struct {
	Action   string `json:"action"` // set|delete|move|add|mul
	Property string `json:"property"`
	Value    any    `json:"value,omitempty"`
	To       string `json:"to,omitempty"` // move target
}

type Edge struct {
	From string `json:"from"`
	Port string `json:"port,omitempty"` // output port, default "0"
	To   string `json:"to"`
}

type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// Msg is what flows along the edges.
type Msg struct {
	Value    float64        `json:"value"`
	DeviceID string         `json:"device_id"`
	PointID  string         `json:"point_id"`
	Vars     map[string]any `json:"vars"`
}

func (m Msg) clone() Msg {
	c := m
	c.Vars = make(map[string]any, len(m.Vars))
	for k, v := range m.Vars {
		c.Vars[k] = v
	}
	return c
}

// FunctionRunner executes a function node. It must enforce its own limits.
// drop=true means the script returned nothing (the path ends).
type FunctionRunner interface {
	Run(code string, in Msg) (out Msg, drop bool, err error)
}

type ExecOptions struct {
	Functions FunctionRunner // nil: function nodes are disabled
}

type DebugEntry struct {
	Node    string `json:"node"`
	Message string `json:"message"`
}

type ExecResult struct {
	Matched bool
	Actions []Action
	Debug   []DebugEntry
}

var varName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,31}$`)

// ---- property access ----

func (m *Msg) get(prop string) (any, bool) {
	switch prop {
	case "value":
		return m.Value, true
	case "device_id":
		return m.DeviceID, true
	case "point_id":
		return m.PointID, true
	}
	if n, ok := strings.CutPrefix(prop, "vars."); ok {
		v, ok := m.Vars[n]
		return v, ok
	}
	return nil, false
}

func validProp(p string, writable bool) bool {
	if p == "value" {
		return true
	}
	if !writable && (p == "device_id" || p == "point_id") {
		return true
	}
	n, ok := strings.CutPrefix(p, "vars.")
	return ok && varName.MatchString(n)
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, !math.IsNaN(x) && !math.IsInf(x, 0)
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case bool:
		return 0, false
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	}
	return 0, false
}

func ruleMatches(r SwitchRule, v any, have bool) bool {
	if !have {
		return false
	}
	switch r.Op {
	case "contains":
		return strings.Contains(fmt.Sprint(v), fmt.Sprint(r.Value))
	case "==", "!=":
		a, aok := toFloat(v)
		b, bok := toFloat(r.Value)
		eq := false
		if aok && bok {
			eq = a == b
		} else {
			eq = fmt.Sprint(v) == fmt.Sprint(r.Value)
		}
		return eq == (r.Op == "==")
	default:
		a, aok := toFloat(v)
		b, bok := toFloat(r.Value)
		if !aok || !bok {
			return false
		}
		return compare(r.Op, a, b)
	}
}

// render fills {value}, {device_id}, {point_id} and {vars.name}. Anything else
// stays literal; nothing is evaluated.
func render(tpl string, m Msg) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(tpl, '{')
		if i < 0 {
			b.WriteString(tpl)
			break
		}
		j := strings.IndexByte(tpl[i:], '}')
		if j < 0 {
			b.WriteString(tpl)
			break
		}
		b.WriteString(tpl[:i])
		key := tpl[i+1 : i+j]
		if v, ok := m.get(key); ok && validProp(key, false) {
			if f, isf := v.(float64); isf {
				b.WriteString(strconv.FormatFloat(f, 'g', -1, 64))
			} else {
				b.WriteString(fmt.Sprint(v))
			}
		} else {
			b.WriteString(tpl[i : i+j+1])
		}
		tpl = tpl[i+j+1:]
	}
	return b.String()
}

func applyChange(c Change, m *Msg) error {
	switch c.Action {
	case "set":
		if c.Property == "value" {
			f, ok := toFloat(c.Value)
			if !ok {
				return fmt.Errorf("set value needs a number")
			}
			m.Value = f
			return nil
		}
		v := c.Value
		if s, ok := v.(string); ok {
			v = render(s, *m)
		}
		return setVar(m, c.Property, v)
	case "delete":
		if n, ok := strings.CutPrefix(c.Property, "vars."); ok {
			delete(m.Vars, n)
			return nil
		}
		return fmt.Errorf("only vars.* can be deleted")
	case "move":
		v, ok := m.get(c.Property)
		if !ok {
			return nil
		}
		if n, isVar := strings.CutPrefix(c.Property, "vars."); isVar {
			delete(m.Vars, n)
		}
		if c.To == "value" {
			f, ok := toFloat(v)
			if !ok {
				return fmt.Errorf("move to value needs a number")
			}
			m.Value = f
			return nil
		}
		return setVar(m, c.To, v)
	case "add", "mul":
		cur, ok := m.get(c.Property)
		if !ok {
			return nil
		}
		a, aok := toFloat(cur)
		b, bok := toFloat(c.Value)
		if !aok || !bok {
			return fmt.Errorf("%s needs numbers", c.Action)
		}
		r := a + b
		if c.Action == "mul" {
			r = a * b
		}
		if math.IsNaN(r) || math.IsInf(r, 0) {
			return fmt.Errorf("%s overflow", c.Action)
		}
		if c.Property == "value" {
			m.Value = r
			return nil
		}
		return setVar(m, c.Property, r)
	}
	return fmt.Errorf("unknown change action %q", c.Action)
}

func setVar(m *Msg, prop string, v any) error {
	n, ok := strings.CutPrefix(prop, "vars.")
	if !ok || !varName.MatchString(n) {
		return fmt.Errorf("bad property %q", prop)
	}
	if s, isS := v.(string); isS && len(s) > maxStringSize {
		return fmt.Errorf("string too long")
	}
	if _, exists := m.Vars[n]; !exists && len(m.Vars) >= maxVars {
		return fmt.Errorf("too many vars")
	}
	m.Vars[n] = v
	return nil
}

// ---- execution ----

type visit struct {
	node  string
	msg   Msg
	delay time.Duration
}

// execGraph runs the graph for one observed reading. The trigger node decides
// whether the flow starts; every other node only transforms, routes or ends
// the message. Errors in a node end that path and are reported in Debug.
func execGraph(g *Graph, value float64, deviceID, pointID string, opt ExecOptions) ExecResult {
	var res ExecResult
	idx := map[string]*Node{}
	out := map[string][]Edge{}
	for i := range g.Nodes {
		idx[g.Nodes[i].ID] = &g.Nodes[i]
	}
	for _, e := range g.Edges {
		out[e.From] = append(out[e.From], e)
	}
	var start *Node
	for i := range g.Nodes {
		if g.Nodes[i].Type == "trigger" {
			start = &g.Nodes[i]
		}
	}
	if start == nil || !compare(start.Op, value, start.Value) {
		return res
	}
	res.Matched = true
	root := Msg{Value: value, DeviceID: deviceID, PointID: pointID, Vars: map[string]any{}}
	queue := []visit{}
	push := func(from string, port string, m Msg, d time.Duration) {
		for _, e := range out[from] {
			p := e.Port
			if p == "" {
				p = "0"
			}
			if p == port {
				queue = append(queue, visit{e.To, m.clone(), d})
			}
		}
	}
	push(start.ID, "0", root, 0)
	visits := 0
	dbg := func(n *Node, s string) { res.Debug = append(res.Debug, DebugEntry{Node: n.ID, Message: s}) }
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		visits++
		if visits > maxExecNodes {
			res.Debug = append(res.Debug, DebugEntry{Node: "", Message: "run stopped: node visit limit reached"})
			break
		}
		n := idx[v.node]
		if n == nil {
			continue
		}
		m := v.msg
		switch n.Type {
		case "condition":
			if compare(n.Op, m.Value, n.Value) {
				push(n.ID, "0", m, v.delay)
			}
		case "switch":
			pv, have := m.get(n.Property)
			matched := false
			for i, r := range n.Rules {
				hit := r.Op == "else" && !matched || r.Op != "else" && ruleMatches(r, pv, have)
				if !hit {
					continue
				}
				matched = true
				push(n.ID, strconv.Itoa(i), m, v.delay)
				if n.Mode != "all" {
					break
				}
			}
		case "change":
			failed := false
			for _, c := range n.Changes {
				if err := applyChange(c, &m); err != nil {
					dbg(n, "change failed: "+err.Error())
					failed = true
					break
				}
			}
			if !failed {
				push(n.ID, "0", m, v.delay)
			}
		case "delay":
			d := v.delay + time.Duration(n.Seconds)*time.Second
			if d > maxPathDelay*time.Second {
				d = maxPathDelay * time.Second
			}
			push(n.ID, "0", m, d)
		case "function":
			if opt.Functions == nil {
				dbg(n, "function nodes are disabled for this tenant")
				continue
			}
			nm, drop, err := opt.Functions.Run(n.Code, m)
			if err != nil {
				dbg(n, "function error: "+err.Error())
				continue
			}
			if !drop {
				push(n.ID, "0", nm, v.delay)
			}
		case "debug":
			tpl := n.Message
			if tpl == "" {
				tpl = "{value}"
			}
			dbg(n, render(tpl, m))
		case "notify":
			tpl := n.Message
			if tpl == "" {
				tpl = "flow triggered"
			}
			res.Actions = append(res.Actions, Action{ChannelID: n.ChannelID, Message: render(tpl, m), Delay: v.delay})
		}
	}
	return res
}

// ---- validation ----

func validateGraph(g *Graph) error {
	if len(g.Nodes) == 0 || len(g.Nodes) > maxNodes {
		return fmt.Errorf("graph needs 1-%d nodes", maxNodes)
	}
	if len(g.Edges) > maxEdges {
		return fmt.Errorf("at most %d edges", maxEdges)
	}
	idx := map[string]*Node{}
	triggers := 0
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.ID == "" || len(n.ID) > 64 || strings.ContainsAny(n.ID, "<>\x00 ") {
			return fmt.Errorf("node %d: bad id", i)
		}
		if _, dup := idx[n.ID]; dup {
			return fmt.Errorf("duplicate node id %q", n.ID)
		}
		idx[n.ID] = n
		if len(n.Name) > 64 || strings.ContainsAny(n.Name, "<>\x00") {
			return fmt.Errorf("node %s: bad name", n.ID)
		}
		if err := validateNode(n); err != nil {
			return fmt.Errorf("node %s: %w", n.ID, err)
		}
		if n.Type == "trigger" {
			triggers++
		}
	}
	if triggers != 1 {
		return fmt.Errorf("exactly one trigger node required")
	}
	adj := map[string][]string{}
	indeg := map[string]int{}
	for _, e := range g.Edges {
		from, to := idx[e.From], idx[e.To]
		if from == nil || to == nil {
			return fmt.Errorf("edge references an unknown node")
		}
		switch from.Type {
		case "notify", "debug":
			return fmt.Errorf("node %s is terminal and cannot have outputs", from.ID)
		}
		if to.Type == "trigger" {
			return fmt.Errorf("trigger cannot have inputs")
		}
		p := e.Port
		if p == "" {
			p = "0"
		}
		max := 1
		if from.Type == "switch" {
			max = len(from.Rules)
		}
		pn, err := strconv.Atoi(p)
		if err != nil || pn < 0 || pn >= max {
			return fmt.Errorf("node %s has no output port %q", from.ID, p)
		}
		adj[e.From] = append(adj[e.From], e.To)
		indeg[e.To]++
	}
	// acyclic (Kahn) and everything reachable from the trigger
	deg := map[string]int{}
	for k, v := range indeg {
		deg[k] = v
	}
	var q []string
	for _, n := range g.Nodes {
		if deg[n.ID] == 0 {
			q = append(q, n.ID)
		}
	}
	seen := 0
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
		return fmt.Errorf("graph has a cycle")
	}
	reach := map[string]bool{}
	var trig string
	for _, n := range g.Nodes {
		if n.Type == "trigger" {
			trig = n.ID
		}
	}
	stack := []string{trig}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if reach[x] {
			continue
		}
		reach[x] = true
		stack = append(stack, adj[x]...)
	}
	notifies := 0
	for _, n := range g.Nodes {
		if !reach[n.ID] {
			return fmt.Errorf("node %s is not connected to the trigger", n.ID)
		}
		if n.Type == "notify" {
			notifies++
		}
	}
	if notifies == 0 {
		return fmt.Errorf("a flow must notify at least once")
	}
	return nil
}

func validateNode(n *Node) error {
	switch n.Type {
	case "trigger":
		if n.DeviceID == "" || n.PointID == "" || !validOp(n.Op) {
			return ErrBadTrigger
		}
	case "condition":
		if !validOp(n.Op) {
			return fmt.Errorf("bad op")
		}
	case "switch":
		if !validProp(n.Property, false) {
			return fmt.Errorf("bad property")
		}
		if len(n.Rules) == 0 || len(n.Rules) > 10 {
			return fmt.Errorf("1-10 rules")
		}
		if n.Mode != "" && n.Mode != "first" && n.Mode != "all" {
			return fmt.Errorf("mode must be first or all")
		}
		for _, r := range n.Rules {
			switch r.Op {
			case "else":
			case "contains", "==", "!=", ">", "<", ">=", "<=":
				if s, ok := r.Value.(string); ok && len(s) > maxStringSize {
					return fmt.Errorf("rule value too long")
				}
				switch r.Value.(type) {
				case string, float64, int, int64:
				default:
					return fmt.Errorf("rule value must be a string or number")
				}
			default:
				return fmt.Errorf("bad rule op %q", r.Op)
			}
		}
	case "change":
		if len(n.Changes) == 0 || len(n.Changes) > 10 {
			return fmt.Errorf("1-10 changes")
		}
		for _, c := range n.Changes {
			switch c.Action {
			case "set", "add", "mul":
				if !validProp(c.Property, true) {
					return fmt.Errorf("bad property %q", c.Property)
				}
				if c.Action != "set" || c.Property == "value" {
					if _, ok := toFloat(c.Value); !ok {
						return fmt.Errorf("%s needs a number", c.Action)
					}
				}
				switch c.Value.(type) {
				case string, float64, int, int64, bool:
				default:
					return fmt.Errorf("change value must be string, number or boolean")
				}
				if s, ok := c.Value.(string); ok && len(s) > maxStringSize {
					return fmt.Errorf("change value too long")
				}
			case "delete":
				if !strings.HasPrefix(c.Property, "vars.") || !validProp(c.Property, true) {
					return fmt.Errorf("only vars.* can be deleted")
				}
			case "move":
				if !validProp(c.Property, false) || !validProp(c.To, true) {
					return fmt.Errorf("bad move")
				}
			default:
				return fmt.Errorf("unknown change action %q", c.Action)
			}
		}
	case "delay":
		if n.Seconds < 1 || n.Seconds > 3600 {
			return fmt.Errorf("delay 1-3600 seconds")
		}
	case "notify":
		if n.ChannelID == "" {
			return fmt.Errorf("channel_id required")
		}
		if len(n.Message) > 500 {
			return fmt.Errorf("message too long")
		}
	case "debug":
		if len(n.Message) > 500 {
			return fmt.Errorf("message too long")
		}
	case "function":
		if strings.TrimSpace(n.Code) == "" || len(n.Code) > maxFuncCode {
			return fmt.Errorf("function code required, max %d bytes", maxFuncCode)
		}
	default:
		return fmt.Errorf("unknown node type %q", n.Type)
	}
	return nil
}

// ToGraph converts a legacy linear definition into the equivalent graph. It is
// how existing flows open in the visual editor; stored flows are not rewritten.
func ToGraph(d Definition) Graph {
	if d.Graph != nil {
		return *d.Graph
	}
	g := Graph{Nodes: []Node{{ID: "trigger", Type: "trigger", DeviceID: d.Trigger.DeviceID, PointID: d.Trigger.PointID, Op: d.Trigger.Op, Value: d.Trigger.Value, X: 40, Y: 40}}}
	prev := "trigger"
	for i, st := range d.Steps {
		n := Node{ID: "s" + strconv.Itoa(i), Type: st.Type, X: 40 + float64(i+1)*200, Y: 40, Op: st.Op, Value: st.Value, Seconds: st.Seconds, ChannelID: st.ChannelID, Message: st.Message}
		g.Nodes = append(g.Nodes, n)
		g.Edges = append(g.Edges, Edge{From: prev, To: n.ID})
		prev = n.ID
	}
	return g
}

// MarshalGraph is a convenience for tests and tools.
func MarshalGraph(g Graph) []byte { b, _ := json.Marshal(g); return b }
