// Package assistant holds the rules for what the AI assistant may ask the platform to do. It is a
// default-deny policy: reads are allowed except for credential-bearing paths; changes are allowed
// only for an explicit list of operations and only after the signed-in user confirms the exact
// action. The policy runs on the server, so nothing a model says can widen it.
package assistant

import (
	"regexp"
	"strings"
)

type Verdict int

const (
	Denied  Verdict = iota
	Read            // run now
	Confirm         // park until the user confirms
)

// readDenied are paths the assistant may not even read: credentials and its own settings.
var readDenied = []*regexp.Regexp{
	regexp.MustCompile(`^/v1/secrets(/|$)`),
	regexp.MustCompile(`^/v1/api-keys(/|$)`),
	regexp.MustCompile(`^/v1/devices/[^/]+/tokens(/|$)`),
	regexp.MustCompile(`^/v1/direct-auth(/|$)`),
	regexp.MustCompile(`^/v1/direct-devices(/|$)`),
	regexp.MustCompile(`^/v1/enrollment(/|$)`),
	regexp.MustCompile(`^/v1/ai(/|$)`),
	regexp.MustCompile(`^/v1/assistant(/|$)`),
	regexp.MustCompile(`^/v1/reports/[^/]+/download$`), // binary files
	regexp.MustCompile(`^/v1/commissioning(/|$)`),
}

type writeRule struct {
	method string
	re     *regexp.Regexp
	what   string
}

func w(method, pattern, what string) writeRule {
	return writeRule{method, regexp.MustCompile("^" + pattern + "$"), what}
}

// writeAllowed is the complete list of changes the assistant may propose. Anything not listed is
// refused: users and roles, API keys, secrets, SSO, feature switches, control targets (what may be
// actuated and whether it is automatic), approving or rejecting commands, data retention, broker
// ACLs, device credentials, firmware rollout steps, edge-rule pushes, fake telemetry ingest and
// the assistant's own settings stay human-only.
var writeAllowed = []writeRule{
	w("POST", `/v1/devices`, "create a device"),
	w("POST", `/v1/devices/bulk`, "create several devices"),
	w("PUT", `/v1/devices/[^/]+/tags`, "set device tags"),
	w("PUT", `/v1/devices/[^/]+/asset`, "attach a device to an asset"),
	w("PUT", `/v1/devices/[^/]+/attributes`, "set device attributes"),
	w("POST", `/v1/assets`, "create an asset"),
	w("DELETE", `/v1/assets/[^/]+`, "delete an asset"),
	w("POST", `/v1/relations`, "link two assets"),
	w("DELETE", `/v1/relations`, "remove an asset link"),
	w("POST", `/v1/kpis`, "create a KPI"),
	w("DELETE", `/v1/kpis/[^/]+`, "delete a KPI"),
	w("POST", `/v1/rules`, "create an alert rule"),
	w("POST", `/v1/alerts/[^/]+/(ack|resolve|assign|comments)`, "act on an alert"),
	w("POST", `/v1/maintenance`, "schedule a maintenance window"),
	w("POST", `/v1/maintenance/[^/]+/end`, "end a maintenance window"),
	w("PUT", `/v1/escalation`, "replace the escalation policy"),
	w("POST", `/v1/oncall`, "create an on-call rotation"),
	w("DELETE", `/v1/oncall/[^/]+`, "delete an on-call rotation"),
	w("POST", `/v1/notifications/channels`, "add a notification channel"),
	w("POST", `/v1/dashboards`, "create a dashboard"),
	w("POST", `/v1/dashboards/import`, "import a dashboard"),
	w("PUT", `/v1/dashboards/[^/]+`, "edit a dashboard"),
	w("DELETE", `/v1/dashboards/[^/]+`, "delete a dashboard"),
	w("POST", `/v1/reports`, "create a report"),
	w("PUT", `/v1/reports/[^/]+`, "edit a report"),
	w("POST", `/v1/reports/[^/]+/run`, "run a report"),
	w("POST", `/v1/reports/[^/]+/versions/[^/]+/restore`, "restore a report version"),
	w("POST", `/v1/flows`, "create a flow"),
	w("POST", `/v1/flows/import`, "import a flow"),
	w("POST", `/v1/flows/[^/]+/(draft|publish|rollback|duplicate)`, "change a flow"),
	w("PATCH", `/v1/flows/[^/]+`, "edit a flow"),
	w("DELETE", `/v1/flows/[^/]+`, "delete a flow"),
	w("POST", `/v1/commands`, "raise a control request (a different person must approve it)"),
	w("PUT", `/v1/branding`, "change branding"),
	w("POST", `/v1/fleet/releases`, "register a firmware release"),
	w("POST", `/v1/profiles`, "create a device profile"),
}

// Classify decides what to do with a request the model wants to make.
func Classify(method, path string) (Verdict, string) {
	method = strings.ToUpper(method)
	if !strings.HasPrefix(path, "/v1/") || strings.Contains(path, "..") || strings.ContainsAny(path, "?#\\ \x00") || strings.Contains(path, "//") {
		return Denied, "path must be a plain /v1/ path (put query parameters in the query field)"
	}
	for _, re := range readDenied {
		if re.MatchString(path) {
			return Denied, "the assistant may not use this part of the API"
		}
	}
	switch method {
	case "GET":
		return Read, ""
	case "POST", "PUT", "PATCH", "DELETE":
		for _, r := range writeAllowed {
			if r.method == method && r.re.MatchString(path) {
				return Confirm, r.what
			}
		}
		return Denied, "the assistant may not make this change; it stays a human action in the normal UI"
	}
	return Denied, "unsupported method"
}

// ReadCatalog is a short list of useful read endpoints, given to the model so it does not have to guess.
var ReadCatalog = []string{
	"GET /v1/devices (list), GET /v1/devices/{id}/health, GET /v1/devices/{id}/shadow",
	"GET /v1/alerts?status=open, GET /v1/alerts/{id}, GET /v1/alerts/{id}/root-cause",
	"GET /v1/telemetry/series?device_id=&point_id=&hours=, /v1/telemetry/rollup, /v1/telemetry/anomalies, /v1/telemetry/forecast, /v1/telemetry/related",
	"GET /v1/fleet, GET /v1/gateways, GET /v1/assets, GET /v1/relations?kind=asset&id=, GET /v1/relations/downstream",
	"GET /v1/kpis, GET /v1/rules, GET /v1/dashboards, GET /v1/reports, GET /v1/flows, GET /v1/maintenance",
	"GET /v1/escalation, GET /v1/oncall, GET /v1/notifications/channels, GET /v1/commands, GET /v1/control-targets, GET /v1/audit",
	"POST /v1/ask with {question} answers a few fixed questions",
}

var lowRisk = []writeRule{
	w("POST", `/v1/alerts/[^/]+/(ack|comments)`, "acknowledge or comment on an alert"),
}

// LowRisk reports whether a change is in the small set an admin may let run without a confirm
// (per linked chat identity, off by default): acknowledging or commenting on an alert.
func LowRisk(method, path string) bool {
	method = strings.ToUpper(method)
	for _, r := range lowRisk {
		if r.method == method && r.re.MatchString(path) {
			return true
		}
	}
	return false
}
