// Package aitools is the typed tool registry the AI assistant works through. Each tool is a named,
// schema-checked wrapper over one platform API operation, with a fixed risk level. A model can only
// name a tool and fill its arguments; the registry validates them strictly (unknown fields, wrong
// types, values out of range, bad characters are all refused) and builds the request itself, so the
// model never writes a path. The request then goes through the same policy, the same user
// permissions and the same confirm gate as everything else. Only operations that exist are listed.
package aitools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/llm"
)

// Risk is fixed per tool by the platform, never by the model.
type Risk string

const (
	Read          Risk = "READ"            // runs at once
	LowRiskWrite  Risk = "LOW_RISK_WRITE"  // proposed; an admin may allow it without a confirm per chat link
	HighRiskWrite Risk = "HIGH_RISK_WRITE" // proposed; always confirmed by the user
	Destructive   Risk = "DESTRUCTIVE"     // proposed; always confirmed, flagged as irreversible
)

type Param struct {
	Name     string
	Kind     string // string | int | enum
	Required bool
	Desc     string
	Enum     []string
	Min, Max int    // int range, or string length (Max) when Kind is string
	In       string // path | query | body
}

type Tool struct {
	Name        string
	Description string
	Risk        Risk
	Method      string
	Path        string // may contain {param} placeholders, filled from path params
	Params      []Param
	Impact      string // shown to the user for writes; {param} placeholders filled from arguments
}

// Call is a validated, fully built request.
type Call struct {
	Tool   *Tool
	Method string
	Path   string
	Query  map[string]string
	Body   string // JSON, empty for none
	Impact string
}

var idRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func idp(name, desc string) Param {
	return Param{Name: name, Kind: "string", Required: true, Desc: desc, Max: 128, In: "path"}
}

// All lists every tool, in the order the model sees them.
var all = []*Tool{
	{Name: "list_devices", Description: "List devices. Filter by text, tag, group or asset.", Risk: Read, Method: "GET", Path: "/v1/devices",
		Params: []Param{{Name: "q", Kind: "string", Max: 80, In: "query", Desc: "text to search for"}, {Name: "tag", Kind: "string", Max: 64, In: "query"},
			{Name: "group_id", Kind: "string", Max: 128, In: "query"}, {Name: "asset_id", Kind: "string", Max: 128, In: "query"}}},
	{Name: "get_device_health", Description: "Health of one device: last seen, quality, staleness.", Risk: Read, Method: "GET", Path: "/v1/devices/{device_id}/health",
		Params: []Param{idp("device_id", "device id")}},
	{Name: "get_device_state", Description: "Latest reported and desired state (shadow) of one device.", Risk: Read, Method: "GET", Path: "/v1/devices/{device_id}/shadow",
		Params: []Param{idp("device_id", "device id")}},
	{Name: "list_alerts", Description: "List alerts by status.", Risk: Read, Method: "GET", Path: "/v1/alerts",
		Params: []Param{{Name: "status", Kind: "enum", Enum: []string{"open", "acknowledged", "resolved"}, In: "query", Desc: "default open"}, {Name: "asset_id", Kind: "string", Max: 128, In: "query"}}},
	{Name: "get_alert", Description: "One alert with its comments.", Risk: Read, Method: "GET", Path: "/v1/alerts/{alert_id}", Params: []Param{idp("alert_id", "alert id")}},
	{Name: "explain_alert", Description: "Likely related signals for an alert (correlation, not proof of cause).", Risk: Read, Method: "GET", Path: "/v1/alerts/{alert_id}/root-cause",
		Params: []Param{idp("alert_id", "alert id")}},
	{Name: "get_telemetry", Description: "Time series of one point on one device.", Risk: Read, Method: "GET", Path: "/v1/telemetry/series",
		Params: []Param{{Name: "device_id", Kind: "string", Required: true, Max: 128, In: "query"}, {Name: "point_id", Kind: "string", Required: true, Max: 128, In: "query"},
			{Name: "hours", Kind: "int", Min: 1, Max: 720, In: "query", Desc: "window, default 24"}}},
	{Name: "latest_values", Description: "Latest value of every point on one device.", Risk: Read, Method: "GET", Path: "/v1/telemetry/latest",
		Params: []Param{{Name: "device_id", Kind: "string", Required: true, Max: 128, In: "query"}}},
	{Name: "find_anomalies", Description: "Statistical anomalies in one point over a window (labelled statistics, not a diagnosis).", Risk: Read, Method: "GET", Path: "/v1/telemetry/anomalies",
		Params: []Param{{Name: "device_id", Kind: "string", Required: true, Max: 128, In: "query"}, {Name: "point_id", Kind: "string", Required: true, Max: 128, In: "query"},
			{Name: "hours", Kind: "int", Min: 1, Max: 720, In: "query", Desc: "window, default 24"}}},
	{Name: "forecast_point", Description: "Statistical forecast of one point with its backtest accuracy. A projection, not a promise.", Risk: Read, Method: "GET", Path: "/v1/telemetry/forecast",
		Params: []Param{{Name: "device_id", Kind: "string", Required: true, Max: 128, In: "query"}, {Name: "point_id", Kind: "string", Required: true, Max: 128, In: "query"}}},
	{Name: "related_signals", Description: "Points that move together with one point (correlation only).", Risk: Read, Method: "GET", Path: "/v1/telemetry/related",
		Params: []Param{{Name: "device_id", Kind: "string", Required: true, Max: 128, In: "query"}, {Name: "point_id", Kind: "string", Required: true, Max: 128, In: "query"}}},
	{Name: "list_kpis", Description: "KPIs with their current values.", Risk: Read, Method: "GET", Path: "/v1/kpis"},
	{Name: "list_groups", Description: "Device groups.", Risk: Read, Method: "GET", Path: "/v1/groups"},
	{Name: "list_maintenance_windows", Description: "Active and recent maintenance windows (alerts are held while one is on).", Risk: Read, Method: "GET", Path: "/v1/maintenance"},
	{Name: "downstream_impact", Description: "What depends on an asset or device (downstream relations).", Risk: Read, Method: "GET", Path: "/v1/relations/downstream",
		Params: []Param{{Name: "kind", Kind: "enum", Enum: []string{"asset", "device"}, Required: true, In: "query"}, {Name: "id", Kind: "string", Required: true, Max: 128, In: "query"},
			{Name: "relation", Kind: "string", Required: true, Max: 40, In: "query", Desc: "relation name, for example feeds"},
			{Name: "depth", Kind: "int", Min: 1, Max: 10, In: "query"}}},
	{Name: "search_docs", Description: "Search the platform's own documentation (setup, security, deployment, assistant, connectors). Use it for how-to and what-is questions; cite the doc and heading.", Risk: Read, Method: "GET", Path: "/v1/docs/search",
		Params: []Param{{Name: "q", Kind: "string", Required: true, Max: 300, In: "query", Desc: "the question, in a few words"}}},
	{Name: "investigate_scope", Description: "Investigate a site or asset by name (for example the north plant). Collects its devices, gateways and open alerts and returns findings computed by fixed rules: problem, evidence, likely cause, severity, recommended action, possible fix. Use this first for 'something is wrong at X'; then explain the result, do not recompute it.", Risk: Read, Method: "GET", Path: "/v1/diagnostics/investigate",
		Params: []Param{{Name: "scope", Kind: "string", Required: true, Max: 80, In: "query", Desc: "site or asset name or part of it"}}},
	{Name: "list_reports", Description: "List the saved reports the user can see (id, name, schedule).", Risk: Read, Method: "GET", Path: "/v1/reports"},
	{Name: "offer_report_download", Description: "Prepare a saved report as a file for the user to download. The file is NOT shown to you; the user gets a download button under your answer. Call list_reports first to get the id.", Risk: Read, Method: "GET", Path: "/v1/reports/{report_id}/download",
		Params: []Param{idp("report_id", "report id from list_reports"), {Name: "format", Kind: "enum", Enum: []string{"csv", "html", "pdf", "xlsx"}, Required: true, In: "query", Desc: "file format"}}},
	{Name: "fleet_summary", Description: "Fleet status counts: online, stale, offline devices and open alerts.", Risk: Read, Method: "GET", Path: "/v1/fleet"},
	{Name: "list_gateways", Description: "Gateways with their last-seen state.", Risk: Read, Method: "GET", Path: "/v1/gateways"},
	{Name: "list_assets", Description: "Assets (sites, lines, machines).", Risk: Read, Method: "GET", Path: "/v1/assets"},
	{Name: "list_commands", Description: "Control commands and their approval state.", Risk: Read, Method: "GET", Path: "/v1/commands",
		Params: []Param{{Name: "status", Kind: "string", Max: 32, In: "query"}}},
	{Name: "list_rules", Description: "Alert rules.", Risk: Read, Method: "GET", Path: "/v1/rules"},
	{Name: "recent_audit", Description: "Recent audit log entries.", Risk: Read, Method: "GET", Path: "/v1/audit",
		Params: []Param{{Name: "asset_id", Kind: "string", Max: 128, In: "query"}}},
	{Name: "acknowledge_alert", Description: "Acknowledge one alert. Proposed, not done, until the user confirms.", Risk: LowRiskWrite, Method: "POST", Path: "/v1/alerts/{alert_id}/ack",
		Params: []Param{idp("alert_id", "alert id")}, Impact: "Acknowledge alert {alert_id}. It stays visible and can be resolved later."},
	{Name: "comment_on_alert", Description: "Add a comment to one alert. Proposed until the user confirms.", Risk: LowRiskWrite, Method: "POST", Path: "/v1/alerts/{alert_id}/comments",
		Params: []Param{idp("alert_id", "alert id"), {Name: "body", Kind: "string", Required: true, Max: 1000, In: "body", Desc: "comment text"}},
		Impact: "Add a comment to alert {alert_id}: \"{body}\". Comments cannot be edited."},
	{Name: "create_site", Description: "Create a site (a physical location devices belong to). Proposed until the user confirms. A site has only a name and an optional address; there is no colour or other setting.", Risk: HighRiskWrite, Method: "POST", Path: "/v1/sites",
		Params: []Param{{Name: "name", Kind: "string", Required: true, Max: 80, In: "body", Desc: "site name"}, {Name: "address", Kind: "string", Max: 200, In: "body", Desc: "optional address"}},
		Impact: "Create the site \"{name}\". Admins only; it cannot be renamed or deleted from the UI yet."},
	{Name: "create_asset", Description: "Create an asset (plant, line, machine or room) in the asset tree. Proposed until the user confirms.", Risk: HighRiskWrite, Method: "POST", Path: "/v1/assets",
		Params: []Param{{Name: "name", Kind: "string", Required: true, Max: 80, In: "body"}, {Name: "kind", Kind: "enum", Enum: []string{"plant", "line", "machine", "room", "asset"}, Required: true, In: "body"},
			{Name: "parent_id", Kind: "string", Max: 128, In: "body", Desc: "optional parent asset id from list_assets"}},
		Impact: "Create the {kind} \"{name}\" in the asset tree."},
	{Name: "create_customer", Description: "Create a customer (an external organisation whose users see only their own devices). Proposed until the user confirms. Scoping a user to a customer is done in the Customers page.", Risk: HighRiskWrite, Method: "POST", Path: "/v1/customers",
		Params: []Param{{Name: "name", Kind: "string", Required: true, Max: 80, In: "body"}, {Name: "parent_id", Kind: "string", Max: 128, In: "body", Desc: "optional parent customer id"}},
		Impact: "Create the customer \"{name}\"."},
	{Name: "create_group", Description: "Create a device group. Proposed until the user confirms. Devices are added to it afterwards.", Risk: HighRiskWrite, Method: "POST", Path: "/v1/groups",
		Params: []Param{{Name: "name", Kind: "string", Required: true, Max: 80, In: "body"}, {Name: "description", Kind: "string", Max: 300, In: "body"}},
		Impact: "Create the device group \"{name}\"."},
}

var byName = func() map[string]*Tool {
	m := map[string]*Tool{}
	for _, t := range all {
		m[t.Name] = t
	}
	return m
}()

// Names returns the tool names, sorted.
func Names() []string {
	var n []string
	for _, t := range all {
		n = append(n, t.Name)
	}
	sort.Strings(n)
	return n
}

// Lookup returns a tool by name.
func Lookup(name string) (*Tool, bool) { t, ok := byName[name]; return t, ok }

// Specs returns the model-facing definitions.
func Specs() []llm.Tool {
	var out []llm.Tool
	for _, t := range all {
		props := map[string]any{}
		var req []string
		for _, p := range t.Params {
			d := map[string]any{"description": p.Desc}
			switch p.Kind {
			case "int":
				d["type"] = "integer"
				if p.Max > 0 {
					d["minimum"], d["maximum"] = p.Min, p.Max
				}
			case "enum":
				d["type"], d["enum"] = "string", p.Enum
			default:
				d["type"] = "string"
			}
			props[p.Name] = d
			if p.Required {
				req = append(req, p.Name)
			}
		}
		schema := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if len(req) > 0 {
			schema["required"] = req
		}
		desc := t.Description + " [" + string(t.Risk) + "]"
		out = append(out, llm.Tool{Name: t.Name, Description: desc, Parameters: schema})
	}
	return out
}

// Resolve validates a model's arguments against the tool's schema and builds the request.
func Resolve(name, argsJSON string) (*Call, error) {
	t, ok := byName[name]
	if !ok {
		return nil, errors.New("unknown tool")
	}
	if strings.TrimSpace(argsJSON) == "" {
		argsJSON = "{}"
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(argsJSON)))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil || raw == nil {
		return nil, errors.New("arguments must be a JSON object")
	}
	known := map[string]Param{}
	for _, p := range t.Params {
		known[p.Name] = p
	}
	for k := range raw {
		if _, ok := known[k]; !ok {
			return nil, fmt.Errorf("unknown argument %q for %s", k, t.Name)
		}
	}
	vals := map[string]string{}
	for _, p := range t.Params {
		v, present := raw[p.Name]
		if !present || v == nil {
			if p.Required {
				return nil, fmt.Errorf("%s needs %q", t.Name, p.Name)
			}
			continue
		}
		s, err := check(p, v)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", p.Name, err)
		}
		if s == "" && !p.Required {
			continue // an empty optional value means "not given"; never send it
		}
		vals[p.Name] = s
	}
	c := &Call{Tool: t, Method: t.Method, Path: t.Path, Query: map[string]string{}}
	body := map[string]any{}
	for _, p := range t.Params {
		v, ok := vals[p.Name]
		if !ok {
			continue
		}
		switch p.In {
		case "path":
			c.Path = strings.ReplaceAll(c.Path, "{"+p.Name+"}", url.PathEscape(v))
		case "query":
			c.Query[p.Name] = v
		case "body":
			body[p.Name] = v
		}
	}
	if len(body) > 0 {
		b, _ := json.Marshal(body)
		c.Body = string(b)
	}
	if t.Impact != "" {
		imp := t.Impact
		for k, v := range vals {
			imp = strings.ReplaceAll(imp, "{"+k+"}", v)
		}
		c.Impact = imp
	}
	return c, nil
}

func check(p Param, v any) (string, error) {
	switch p.Kind {
	case "int":
		n, ok := v.(json.Number)
		if !ok {
			return "", errors.New("must be a whole number")
		}
		i, err := n.Int64()
		if err != nil {
			return "", errors.New("must be a whole number")
		}
		if p.Max > 0 && (int(i) < p.Min || int(i) > p.Max) {
			return "", fmt.Errorf("must be between %d and %d", p.Min, p.Max)
		}
		return fmt.Sprint(i), nil
	case "enum":
		s, ok := v.(string)
		if !ok {
			return "", errors.New("must be text")
		}
		for _, e := range p.Enum {
			if s == e {
				return s, nil
			}
		}
		return "", fmt.Errorf("must be one of %s", strings.Join(p.Enum, ", "))
	}
	s, ok := v.(string)
	if !ok {
		return "", errors.New("must be text")
	}
	if len(s) > p.Max {
		return "", fmt.Errorf("longer than %d characters", p.Max)
	}
	if p.Required && s == "" {
		return "", errors.New("is empty")
	}
	if p.In == "path" || strings.HasSuffix(p.Name, "_id") || p.Name == "id" {
		if s != "" && !idRE.MatchString(s) {
			return "", errors.New("is not a valid id")
		}
	} else if strings.ContainsAny(s, "\x00\r") {
		return "", errors.New("contains control characters")
	}
	return s, nil
}

// SpecsFor returns a short list of tool definitions for one user message: a small base set plus
// the groups whose keywords appear in the text, at most max tools, with descriptions cut to the
// first sentence. It exists so a small local model's prompt fits its context window. Every tool is
// still callable by name (Resolve does not depend on this list).
func SpecsFor(text string, max int) []llm.Tool {
	groups := []struct {
		words []string
		tools []string
	}{
		{[]string{"device", "health", "state", "value", "reading", "online", "offline", "stale", "temperature", "twin"}, []string{"get_device_health", "get_device_state", "latest_values"}},
		{[]string{"telemetry", "series", "history", "trend", "hours", "last 24", "chart"}, []string{"get_telemetry", "latest_values"}},
		{[]string{"anomal", "abnormal", "spike", "unusual"}, []string{"find_anomalies"}},
		{[]string{"forecast", "predict", "projection"}, []string{"forecast_point"}},
		{[]string{"correlat", "related", "together"}, []string{"related_signals"}},
		{[]string{"alert", "alarm", "ack", "acknowledge", "comment", "why did"}, []string{"get_alert", "explain_alert", "acknowledge_alert", "comment_on_alert"}},
		{[]string{"wrong", "problem", "issue", "investigate", "fix", "diagnos", "plant", "site"}, []string{"investigate_scope"}},
		{[]string{"create", "add a", "new site", "new group", "new asset", "new customer", "make a"}, []string{"create_site", "create_asset", "create_customer", "create_group"}},
		{[]string{"asset", "line", "machine", "downstream", "depends"}, []string{"list_assets", "downstream_impact"}},
		{[]string{"group"}, []string{"list_groups"}},
		{[]string{"report", "export", "download", "csv", "pdf"}, []string{"list_reports", "offer_report_download"}},
		{[]string{"kpi", "oee"}, []string{"list_kpis"}},
		{[]string{"gateway"}, []string{"list_gateways"}},
		{[]string{"rule"}, []string{"list_rules"}},
		{[]string{"maintenance"}, []string{"list_maintenance_windows"}},
		{[]string{"command", "control"}, []string{"list_commands"}},
		{[]string{"audit", "who changed"}, []string{"recent_audit"}},
	}
	low := strings.ToLower(text)
	want := []string{"search_docs", "fleet_summary", "list_alerts", "list_devices"}
	for _, g := range groups {
		for _, w := range g.words {
			if strings.Contains(low, w) {
				want = append(want, g.tools...)
				break
			}
		}
	}
	seen := map[string]bool{}
	var out []llm.Tool
	all := Specs()
	idx := map[string]llm.Tool{}
	for _, t := range all {
		idx[t.Name] = t
	}
	for _, n := range want {
		t, ok := idx[n]
		if !ok || seen[n] || len(out) >= max {
			continue
		}
		seen[n] = true
		d := t.Description
		if i := strings.Index(d, ". "); i > 0 && i < 140 {
			d = d[:i+1] + d[strings.LastIndex(d, " ["):]
		}
		t.Description = d
		out = append(out, t)
	}
	return out
}
