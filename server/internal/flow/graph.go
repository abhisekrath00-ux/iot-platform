package flow

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
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

const maxSplit = 100

func isStart(t string) bool { return t == "trigger" || t == "inject" }

type Node struct {
	ID   string  `json:"id"`
	Type string  `json:"type"` // trigger|switch|change|condition|delay|notify|debug|function
	Name string  `json:"name,omitempty"`
	X    float64 `json:"x,omitempty"` // editor position only
	Y    float64 `json:"y,omitempty"`

	// subflow: a live reference to a saved fragment, expanded by the server before storing (see subflow.go)
	FragmentID      string `json:"fragment_id,omitempty"`
	FragmentVersion int    `json:"fragment_version,omitempty"`

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
	// template: renders Template into vars.<Target> (Target is a bare var name)
	Template string `json:"template,omitempty"`
	Target   string `json:"target,omitempty"`
	// range: linear rescale of value from [in_min,in_max] to [out_min,out_max]
	InMin  float64 `json:"in_min,omitempty"`
	InMax  float64 `json:"in_max,omitempty"`
	OutMin float64 `json:"out_min,omitempty"`
	OutMax float64 `json:"out_max,omitempty"`
	Clamp  bool    `json:"clamp,omitempty"`
	// http: outbound request whose answer lands in vars.<Target>. Port 0 = success, port 1 = failure.
	Method  string `json:"method,omitempty"`  // GET | POST
	URL     string `json:"url,omitempty"`     // static http(s) URL, no credentials, no templating
	Body    string `json:"body,omitempty"`    // POST body template ({value}, {vars.x}), sent as application/json
	Extract string `json:"extract,omitempty"` // dot path into a JSON response, e.g. main.temp; empty = whole body
	// control: ask for a change to an allowlisted target (never actuates; needs approval). Port 0 = request raised, 1 = refused.
	// The value is Value, or the message value when UseValue is set. The target checks it; nothing is clamped.
	// split: Property names a list (for example vars.items from an http node); one message per element.
	// join: collects every message that reaches it in this run and emits one: Mode list|sum|avg|min|max|count, result in vars.<Target>.
	TargetID string `json:"target_id,omitempty"`
	UseValue bool   `json:"use_value,omitempty"`
	// context: numeric state that survives between runs. Mode get|set|incr, Scope flow|global, Key a name.
	// get stores the value in vars.<Target> (port 0, or port 1 when the key is unset); set and incr use Value, or
	// the message value when UseValue is set, and incr returns the new total in vars.<Target> when Target is given.
	Scope string `json:"scope,omitempty"`
	Key   string `json:"key,omitempty"`
}

// ContextStore keeps flow and global context values. nil means context nodes are off (dry runs).
// Values are numbers only, bounded per scope, and tenant-isolated by the implementation.
type ContextStore interface {
	Get(scope, key string) (float64, bool, error)
	Set(scope, key string, v float64) error
	Incr(scope, key string, d float64) (float64, error)
}

const maxContextKeys = 100

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
	Functions FunctionRunner   // nil: function nodes are disabled
	Limiter   Limiter          // nil: rate-limit nodes pass everything (simulation)
	LimitKey  string           // scopes limiter state, e.g. tenant/flow id
	Scheduled bool             // run an inject-started graph (the scheduler sets this)
	HTTP      HTTPDoer         // nil: http nodes are disabled (feature off, or a dry run)
	Context   ContextStore     // nil: context nodes pass through unchanged (dry run)
	Control   ControlRequester // nil: control nodes raise nothing (feature off, or a dry run)
}

// HTTPDoer performs one outbound request for an http node. Implementations must
// enforce the SSRF rules (no loopback/link-local/metadata addresses, no redirects),
// a short timeout and a response size cap.
type HTTPDoer interface {
	HTTPDo(ctx context.Context, method, url string, body []byte) (status int, resp []byte, err error)
}

const (
	maxHTTPPerRun = 2
	maxHTTPResp   = 16 << 10
)

// Limiter decides whether a rate-limit node lets a message through. State is
// per process: with several API replicas each keeps its own window.
type Limiter interface {
	Allow(key string, every time.Duration) bool
}

// MemLimiter allows one message per key per window, in memory.
type MemLimiter struct {
	mu   sync.Mutex
	last map[string]time.Time
	Now  func() time.Time
}

func (l *MemLimiter) Allow(key string, every time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.Now != nil {
		now = l.Now()
	}
	if l.last == nil {
		l.last = map[string]time.Time{}
	}
	if t, ok := l.last[key]; ok && now.Sub(t) < every {
		return false
	}
	if len(l.last) > 10000 { // bound memory: drop expired entries
		for k, t := range l.last {
			if now.Sub(t) >= time.Hour {
				delete(l.last, k)
			}
		}
	}
	l.last[key] = now
	return true
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
			// Not a placeholder: emit the brace and rescan after it, so JSON
			// bodies like {"v":{value}} still resolve the inner placeholder.
			b.WriteByte('{')
			tpl = tpl[i+1:]
			continue
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
		if isStart(g.Nodes[i].Type) {
			start = &g.Nodes[i]
		}
	}
	if start == nil {
		return res
	}
	if start.Type == "inject" {
		// timed start: only the scheduler runs it, never an incoming reading
		if !opt.Scheduled {
			return res
		}
		value, deviceID, pointID = start.Value, start.DeviceID, start.PointID
	} else if opt.Scheduled || !compare(start.Op, value, start.Value) {
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
	httpCalls, controlCalls := 0, 0
	dbg := func(n *Node, s string) { res.Debug = append(res.Debug, DebugEntry{Node: n.ID, Message: s}) }
	type joinBuf struct {
		vals  []float64
		last  Msg
		delay time.Duration
	}
	joins := map[string]*joinBuf{}
	flushJoins := func() bool {
		did := false
		for i := range g.Nodes { // graph order keeps runs deterministic
			n := &g.Nodes[i]
			jb := joins[n.ID]
			if n.Type != "join" || jb == nil || len(jb.vals) == 0 {
				continue
			}
			m := jb.last.clone()
			var list []any
			sum, mn, mx := 0.0, jb.vals[0], jb.vals[0]
			for _, f := range jb.vals {
				list = append(list, f)
				sum += f
				mn, mx = math.Min(mn, f), math.Max(mx, f)
			}
			switch n.Mode {
			case "sum":
				m.Value = sum
			case "avg":
				m.Value = sum / float64(len(jb.vals))
			case "min":
				m.Value = mn
			case "max":
				m.Value = mx
			case "count":
				m.Value = float64(len(jb.vals))
			}
			if n.Mode == "" || n.Mode == "list" {
				m.Vars[n.Target] = list
			} else {
				m.Vars[n.Target] = m.Value
			}
			delete(joins, n.ID)
			push(n.ID, "0", m, jb.delay)
			did = true
		}
		return did
	}
	for {
		if len(queue) == 0 && !flushJoins() {
			break
		}
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
		case "split":
			pv, have := m.get(n.Property)
			list, ok := pv.([]any)
			if !have || !ok {
				dbg(n, "split: "+n.Property+" is not a list")
				continue
			}
			if len(list) > maxSplit {
				list = list[:maxSplit]
				dbg(n, fmt.Sprintf("split: list cut to %d items", maxSplit))
			}
			for i, it := range list {
				c := m.clone()
				c.Vars["item"], c.Vars["index"], c.Vars["count"] = it, float64(i), float64(len(list))
				if f, isNum := toFloat(it); isNum {
					c.Value = f
				}
				push(n.ID, "0", c, v.delay)
			}
		case "join":
			jb := joins[n.ID]
			if jb == nil {
				jb = &joinBuf{}
				joins[n.ID] = jb
			}
			if len(jb.vals) < maxSplit {
				jb.vals = append(jb.vals, m.Value)
			}
			jb.last = m
			if v.delay > jb.delay {
				jb.delay = v.delay
			}
		case "template":
			m = m.clone()
			if err := setVar(&m, "vars."+n.Target, render(n.Template, m)); err != nil {
				dbg(n, "template failed: "+err.Error())
				continue
			}
			push(n.ID, "0", m, v.delay)
		case "range":
			m = m.clone()
			t := (m.Value - n.InMin) / (n.InMax - n.InMin)
			if n.Clamp {
				t = math.Max(0, math.Min(1, t))
			}
			m.Value = n.OutMin + t*(n.OutMax-n.OutMin)
			push(n.ID, "0", m, v.delay)
		case "rate_limit":
			if opt.Limiter == nil || opt.Limiter.Allow(opt.LimitKey+"/"+n.ID, time.Duration(n.Seconds)*time.Second) {
				push(n.ID, "0", m, v.delay)
			} else {
				dbg(n, "dropped by rate limit")
			}
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
		case "http":
			if opt.HTTP == nil {
				dbg(n, "http request not sent: http nodes are disabled for this tenant or this is a dry run")
				push(n.ID, "1", m, v.delay)
				continue
			}
			if httpCalls++; httpCalls > maxHTTPPerRun {
				dbg(n, "http request skipped: per-run limit reached")
				push(n.ID, "1", m, v.delay)
				continue
			}
			var body []byte
			if n.Method == "POST" {
				body = []byte(render(n.Body, m))
			}
			cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			status, resp, err := opt.HTTP.HTTPDo(cctx, n.Method, n.URL, body)
			cancel()
			if err != nil || status < 200 || status > 299 {
				if err != nil {
					dbg(n, "http failed: "+err.Error())
				} else {
					dbg(n, fmt.Sprintf("http status %d", status))
				}
				push(n.ID, "1", m, v.delay)
				continue
			}
			val, perr := httpResult(resp, n.Extract)
			if perr != nil {
				dbg(n, "http response unusable: "+perr.Error())
				push(n.ID, "1", m, v.delay)
				continue
			}
			m = m.clone()
			if setVar(&m, "vars."+n.Target, val) != nil || setVar(&m, "vars."+n.Target+"_status", float64(status)) != nil {
				dbg(n, "http result could not be stored (too long or too many variables)")
				push(n.ID, "1", m, v.delay)
				continue
			}
			push(n.ID, "0", m, v.delay)
		case "context":
			if opt.Context == nil {
				dbg(n, "context not read or written: this is a dry run or context storage is off")
				push(n.ID, "0", m, v.delay)
				continue
			}
			scope := "global"
			if n.Scope != "global" {
				scope = "flow/" + opt.LimitKey
			}
			delta := n.Value
			if n.UseValue {
				delta = m.Value
			}
			var cv float64
			var cok bool
			var cerr error
			switch n.Mode {
			case "get":
				cv, cok, cerr = opt.Context.Get(scope, n.Key)
				if cerr == nil && !cok {
					dbg(n, "context key "+n.Key+" is not set")
					push(n.ID, "1", m, v.delay)
					continue
				}
			case "set":
				cerr = opt.Context.Set(scope, n.Key, delta)
				cv, cok = delta, true
			default:
				cv, cerr = opt.Context.Incr(scope, n.Key, delta)
				cok = true
			}
			if cerr != nil {
				dbg(n, "context failed: "+cerr.Error())
				push(n.ID, "1", m, v.delay)
				continue
			}
			m = m.clone()
			if n.Target != "" && setVar(&m, "vars."+n.Target, cv) != nil {
				dbg(n, "context result could not be stored (too many variables)")
				push(n.ID, "1", m, v.delay)
				continue
			}
			push(n.ID, "0", m, v.delay)
		case "control":
			if opt.Control == nil {
				dbg(n, "control request not raised: control nodes are disabled for this tenant or this is a dry run")
				push(n.ID, "1", m, v.delay)
				continue
			}
			if controlCalls++; controlCalls > maxControlPerRun {
				dbg(n, "control request skipped: per-run limit reached")
				push(n.ID, "1", m, v.delay)
				continue
			}
			val := n.Value
			if n.UseValue {
				val = m.Value
			}
			cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			rid, note, err := opt.Control.RequestControl(cctx, n.TargetID, val)
			cancel()
			if err != nil {
				dbg(n, "control request refused: "+err.Error())
				push(n.ID, "1", m, v.delay)
				continue
			}
			dbg(n, "control request "+rid+" raised, waiting for approval")
			if note != "" {
				dbg(n, note)
			}
			push(n.ID, "0", m, v.delay)
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
		if isStart(n.Type) {
			triggers++
		}
	}
	if triggers != 1 {
		return fmt.Errorf("exactly one start node (reading trigger or inject) required")
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
		if isStart(to.Type) {
			return fmt.Errorf("a start node cannot have inputs")
		}
		p := e.Port
		if p == "" {
			p = "0"
		}
		max := 1
		if from.Type == "switch" {
			max = len(from.Rules)
		}
		if from.Type == "http" || from.Type == "control" || from.Type == "context" {
			max = 2
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
		if isStart(n.Type) {
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
	case "split":
		if !validProp(n.Property, false) {
			return fmt.Errorf("split needs a property such as vars.items")
		}
	case "join":
		switch n.Mode {
		case "", "list", "sum", "avg", "min", "max", "count":
		default:
			return fmt.Errorf("join mode must be list, sum, avg, min, max or count")
		}
		if n.Target == "" || len(n.Target) > 40 || strings.ContainsAny(n.Target, ". ") {
			return fmt.Errorf("join needs a bare variable name as target")
		}
	case "inject":
		if n.Seconds < 60 || n.Seconds > 86400 {
			return fmt.Errorf("inject interval 60-86400 seconds")
		}
		if math.IsNaN(n.Value) || math.IsInf(n.Value, 0) {
			return fmt.Errorf("inject value must be finite")
		}
	case "rate_limit":
		if n.Seconds < 1 || n.Seconds > 86400 {
			return fmt.Errorf("rate limit window 1-86400 seconds")
		}
	case "template":
		if len(n.Template) == 0 || len(n.Template) > 500 {
			return fmt.Errorf("template required, max 500 bytes")
		}
		if !validProp("vars."+n.Target, true) || n.Target == "" {
			return fmt.Errorf("target must be a variable name")
		}
	case "http":
		if n.Method != "GET" && n.Method != "POST" {
			return fmt.Errorf("method must be GET or POST")
		}
		if err := validHTTPNodeURL(n.URL); err != nil {
			return err
		}
		if len(n.Body) > 500 || (n.Method == "GET" && n.Body != "") {
			return fmt.Errorf("body only for POST, max 500 bytes")
		}
		if n.Target == "" || !validProp("vars."+n.Target, true) {
			return fmt.Errorf("target must be a variable name")
		}
		if len(n.Extract) > 100 || !extractRe.MatchString(n.Extract) {
			return fmt.Errorf("extract must be a dot path like main.temp")
		}
	case "context":
		if n.Mode != "get" && n.Mode != "set" && n.Mode != "incr" {
			return fmt.Errorf("context mode must be get, set or incr")
		}
		if n.Scope != "flow" && n.Scope != "global" {
			return fmt.Errorf("context scope must be flow or global")
		}
		if !varName.MatchString(n.Key) {
			return fmt.Errorf("context key must be a name of letters, digits and underscore")
		}
		if n.Target != "" && !validProp("vars."+n.Target, true) {
			return fmt.Errorf("target must be a variable name")
		}
		if n.Mode == "get" && n.Target == "" {
			return fmt.Errorf("a get needs a target variable")
		}
		if math.IsNaN(n.Value) || math.IsInf(n.Value, 0) {
			return fmt.Errorf("value must be finite")
		}
	case "control":
		if n.TargetID == "" || len(n.TargetID) > 64 || strings.ContainsAny(n.TargetID, "<>\x00 ") {
			return fmt.Errorf("control node needs a target_id")
		}
		if math.IsNaN(n.Value) || math.IsInf(n.Value, 0) {
			return fmt.Errorf("value must be finite")
		}
	case "range":
		if n.InMin == n.InMax {
			return fmt.Errorf("in_min and in_max must differ")
		}
		for _, f := range []float64{n.InMin, n.InMax, n.OutMin, n.OutMax} {
			if math.IsNaN(f) || math.IsInf(f, 0) {
				return fmt.Errorf("range values must be finite")
			}
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

var extractRe = regexp.MustCompile(`^[A-Za-z0-9_.\-]*$`)

func validHTTPNodeURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || len(raw) > 512 || strings.ContainsAny(raw, "{}\x00 ") {
		return fmt.Errorf("url must be a static http(s) address without credentials or placeholders")
	}
	return nil
}

// httpResult turns a response body into one flow value: the number, string or
// boolean at the dot path, or the trimmed body when no path is given.
func httpResult(body []byte, path string) (any, error) {
	if len(body) > maxHTTPResp {
		return nil, fmt.Errorf("response over %d bytes", maxHTTPResp)
	}
	if path == "" {
		t := strings.TrimSpace(string(body))
		if len(t) > maxStringSize {
			return nil, fmt.Errorf("response longer than %d bytes: set an extract path", maxStringSize)
		}
		return t, nil
	}
	var cur any
	if err := json.Unmarshal(body, &cur); err != nil {
		return nil, fmt.Errorf("response is not JSON")
	}
	for _, k := range strings.Split(path, ".") {
		switch x := cur.(type) {
		case map[string]any:
			cur = x[k]
		case []any:
			i, err := strconv.Atoi(k)
			if err != nil || i < 0 || i >= len(x) {
				return nil, fmt.Errorf("path %q not found", path)
			}
			cur = x[i]
		default:
			return nil, fmt.Errorf("path %q not found", path)
		}
	}
	switch x := cur.(type) {
	case float64, bool:
		return x, nil
	case string:
		if len(x) > maxStringSize {
			return nil, fmt.Errorf("value longer than %d bytes", maxStringSize)
		}
		return x, nil
	case []any: // a short list of plain values, for a split node
		if len(x) > maxSplit {
			return nil, fmt.Errorf("list longer than %d items", maxSplit)
		}
		for _, it := range x {
			switch e := it.(type) {
			case float64, bool:
			case string:
				if len(e) > 200 {
					return nil, fmt.Errorf("list item longer than 200 bytes")
				}
			default:
				return nil, fmt.Errorf("path %q: list items must be numbers, strings or booleans", path)
			}
		}
		return x, nil
	}
	return nil, fmt.Errorf("path %q is missing or not a number, string, boolean or list", path)
}
