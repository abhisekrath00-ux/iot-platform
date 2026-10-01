package flow

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Node-RED interchange (a subset, on purpose).
//
// Export writes a graph as a Node-RED flow array. Standard Node-RED node types
// are used where one matches (switch, change, delay, debug, function); the
// trigger and notify nodes have no Node-RED equivalent and are written as
// hexmon-trigger / hexmon-notify, which Node-RED itself cannot run.
//
// Import reads such an array back. Anything outside the supported subset is
// reported and the import is refused, never approximated: an approximate node
// would silently change what an installed flow does.

var swOpToNR = map[string]string{"==": "eq", "!=": "neq", "<": "lt", "<=": "lte", ">": "gt", ">=": "gte", "contains": "cont", "else": "else"}

func nrProp(p string) string {
	if p == "value" {
		return "payload"
	}
	return strings.TrimPrefix(p, "vars.")
}

func fromNRProp(p string) (string, bool) {
	p = strings.TrimPrefix(p, "msg.")
	if p == "payload" {
		return "value", true
	}
	if varName.MatchString(p) {
		return "vars." + p, true
	}
	return "", false
}

func ToNodeRED(g Graph, flowName string) []map[string]any {
	out := []map[string]any{{"id": "tab-hexmon", "type": "tab", "label": flowName}}
	wires := map[string]map[int][]string{}
	for _, e := range g.Edges {
		p, _ := strconv.Atoi(e.Port)
		if wires[e.From] == nil {
			wires[e.From] = map[int][]string{}
		}
		wires[e.From][p] = append(wires[e.From][p], e.To)
	}
	for _, n := range g.Nodes {
		o := map[string]any{"id": n.ID, "z": "tab-hexmon", "name": n.Name, "x": n.X, "y": n.Y}
		ports := 1
		switch n.Type {
		case "trigger":
			o["type"] = "hexmon-trigger"
			o["device_id"], o["point_id"], o["op"], o["value"] = n.DeviceID, n.PointID, n.Op, n.Value
		case "notify":
			o["type"] = "hexmon-notify"
			o["message"], o["channel_id"] = n.Message, n.ChannelID
			ports = 0
		case "debug":
			o["type"] = "debug"
			o["complete"] = "true"
			o["name"] = n.Name
			ports = 0
		case "delay":
			o["type"] = "delay"
			o["pauseType"], o["timeout"], o["timeoutUnits"] = "delay", strconv.Itoa(n.Seconds), "seconds"
		case "condition":
			o["type"] = "switch"
			o["property"] = "payload"
			o["propertyType"] = "msg"
			o["rules"] = []map[string]any{{"t": swOpToNR[n.Op], "v": strconv.FormatFloat(n.Value, 'g', -1, 64), "vt": "num"}}
		case "switch":
			o["type"] = "switch"
			o["property"] = nrProp(n.Property)
			o["propertyType"] = "msg"
			rules := []map[string]any{}
			for _, r := range n.Rules {
				rr := map[string]any{"t": swOpToNR[r.Op]}
				if r.Op != "else" {
					rr["v"] = fmt.Sprint(r.Value)
					if _, isNum := r.Value.(float64); isNum {
						rr["vt"] = "num"
					} else {
						rr["vt"] = "str"
					}
				}
				rules = append(rules, rr)
			}
			o["rules"] = rules
			if n.Mode == "all" {
				o["checkall"] = "true"
			} else {
				o["checkall"] = "false"
			}
			ports = len(n.Rules)
		case "change":
			o["type"] = "change"
			rules := []map[string]any{}
			for _, c := range n.Changes {
				r := map[string]any{"p": nrProp(c.Property), "pt": "msg"}
				switch c.Action {
				case "set":
					r["t"], r["to"] = "set", fmt.Sprint(c.Value)
					if _, isNum := c.Value.(float64); isNum {
						r["tot"] = "num"
					} else {
						r["tot"] = "str"
					}
				case "delete":
					r["t"] = "delete"
				case "move":
					r["t"], r["to"], r["tot"] = "move", nrProp(c.To), "msg"
				case "add", "mul":
					// Node-RED has no add/mul in change; use the function-free form
					r["t"], r["to"], r["tot"] = "hexmon-"+c.Action, fmt.Sprint(c.Value), "num"
				}
				rules = append(rules, r)
			}
			o["rules"] = rules
		case "function":
			o["type"] = "function"
			o["func"] = n.Code
			o["outputs"] = 1
		}
		w := make([][]string, ports)
		for p := 0; p < ports; p++ {
			w[p] = append([]string{}, wires[n.ID][p]...)
			if w[p] == nil {
				w[p] = []string{}
			}
		}
		o["wires"] = w
		out = append(out, o)
	}
	return out
}

// FromNodeRED converts a Node-RED flow array. It returns the sorted list of
// unsupported node types alongside an error when any are present.
func FromNodeRED(nodes []map[string]any) (Graph, []string, error) {
	var g Graph
	unsupported := map[string]bool{}
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	for _, n := range nodes {
		typ := str(n, "type")
		if typ == "tab" {
			continue
		}
		out := Node{ID: str(n, "id"), Name: str(n, "name")}
		if x, ok := n["x"].(float64); ok {
			out.X = x
		}
		if y, ok := n["y"].(float64); ok {
			out.Y = y
		}
		switch typ {
		case "hexmon-trigger":
			out.Type, out.DeviceID, out.PointID, out.Op = "trigger", str(n, "device_id"), str(n, "point_id"), str(n, "op")
			out.Value, _ = n["value"].(float64)
		case "hexmon-notify":
			out.Type, out.Message, out.ChannelID = "notify", str(n, "message"), str(n, "channel_id")
		case "debug":
			out.Type = "debug"
		case "function":
			out.Type, out.Code = "function", str(n, "func")
		case "delay":
			if str(n, "pauseType") != "delay" {
				unsupported["delay("+str(n, "pauseType")+")"] = true
				continue
			}
			secs, err := strconv.ParseFloat(str(n, "timeout"), 64)
			if err != nil {
				unsupported["delay(timeout)"] = true
				continue
			}
			switch str(n, "timeoutUnits") {
			case "minutes":
				secs *= 60
			case "seconds", "":
			default:
				unsupported["delay("+str(n, "timeoutUnits")+")"] = true
				continue
			}
			out.Type, out.Seconds = "delay", int(secs)
		case "switch":
			prop, ok := fromNRProp(str(n, "property"))
			if !ok || str(n, "propertyType") != "msg" && str(n, "propertyType") != "" {
				unsupported["switch(property)"] = true
				continue
			}
			out.Type, out.Property, out.Mode = "switch", prop, "first"
			if str(n, "checkall") == "true" {
				out.Mode = "all"
			}
			rules, _ := n["rules"].([]any)
			for _, ri := range rules {
				r, _ := ri.(map[string]any)
				op := ""
				for k, v := range swOpToNR {
					if v == str(r, "t") {
						op = k
					}
				}
				if op == "" {
					unsupported["switch rule "+str(r, "t")] = true
					continue
				}
				rule := SwitchRule{Op: op}
				if op != "else" {
					if str(r, "vt") == "num" {
						f, err := strconv.ParseFloat(str(r, "v"), 64)
						if err != nil {
							unsupported["switch(value)"] = true
							continue
						}
						rule.Value = f
					} else {
						rule.Value = str(r, "v")
					}
				}
				out.Rules = append(out.Rules, rule)
			}
		case "change":
			out.Type = "change"
			rules, _ := n["rules"].([]any)
			for _, ri := range rules {
				r, _ := ri.(map[string]any)
				p, ok := fromNRProp(str(r, "p"))
				if !ok || (str(r, "pt") != "msg" && str(r, "pt") != "") {
					unsupported["change(property)"] = true
					continue
				}
				c := Change{Property: p}
				switch t := str(r, "t"); t {
				case "set", "hexmon-add", "hexmon-mul":
					c.Action = strings.TrimPrefix(strings.TrimPrefix(t, "hexmon-"), "")
					if str(r, "tot") == "num" {
						f, err := strconv.ParseFloat(str(r, "to"), 64)
						if err != nil {
							unsupported["change(value)"] = true
							continue
						}
						c.Value = f
					} else if str(r, "tot") == "str" || str(r, "tot") == "" {
						c.Value = str(r, "to")
					} else {
						unsupported["change(set "+str(r, "tot")+")"] = true
						continue
					}
				case "delete":
					c.Action = "delete"
				case "move":
					to, ok := fromNRProp(str(r, "to"))
					if !ok {
						unsupported["change(move)"] = true
						continue
					}
					c.Action, c.To = "move", to
				default:
					unsupported["change rule "+t] = true
					continue
				}
				out.Changes = append(out.Changes, c)
			}
		default:
			unsupported[typ] = true
			continue
		}
		g.Nodes = append(g.Nodes, out)
		wires, _ := n["wires"].([]any)
		for p, wi := range wires {
			ws, _ := wi.([]any)
			for _, w := range ws {
				if id, ok := w.(string); ok {
					g.Edges = append(g.Edges, Edge{From: out.ID, Port: strconv.Itoa(p), To: id})
				}
			}
		}
	}
	if len(unsupported) > 0 {
		var l []string
		for k := range unsupported {
			l = append(l, k)
		}
		sort.Strings(l)
		return Graph{}, l, fmt.Errorf("unsupported Node-RED nodes: %s", strings.Join(l, ", "))
	}
	return g, nil, nil
}
