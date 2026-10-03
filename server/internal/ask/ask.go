// Package ask turns a short plain-English question into one of a few fixed read-only queries.
//
// It is phrase matching, not a language model: an air-gapped install has none to call. It
// understands a small, documented set of questions, shows how it read the question, and says so
// when it does not understand. It never writes anything.
package ask

import (
	"regexp"
	"strings"
)

type Kind string

const (
	OpenAlerts    Kind = "open_alerts"
	DeviceAlerts  Kind = "device_alerts"
	LatestValue   Kind = "latest_value"
	OfflineDevice Kind = "offline_devices"
	TaggedDevices Kind = "tagged_devices"
)

// Query is the fixed query a question was read as.
type Query struct {
	Kind     Kind
	Severity string // open_alerts, device_alerts: "" = any
	Device   string // device_alerts, latest_value: the name or id as typed
	Point    string // latest_value
	Tag      string // tagged_devices
}

// Examples are shown to the user when a question is not understood.
var Examples = []string{
	"open alerts",
	"critical alerts",
	"alerts on boiler-1",
	"latest temperature of boiler-1",
	"offline devices",
	"devices tagged boiler",
}

var (
	reLatest = regexp.MustCompile(`^(?:what(?:'s| is) the |what(?:'s| is) |show |get )?(?:latest |current |last )?([a-z0-9_.-]+) (?:of|for|on|at) ([a-z0-9_. -]+)$`)
	reAlerts = regexp.MustCompile(`^(?:show |list |any )?(?:(critical|warning|info) )?alerts? (?:on|for|of) ([a-z0-9_. -]+)$`)
	reTag    = regexp.MustCompile(`^(?:show |list )?devices (?:tagged|with tag|labelled|labeled) ([a-z0-9_.:-]+)$`)
)

var severities = map[string]bool{"critical": true, "warning": true, "info": true}

// Parse reads a question. ok is false when it is not one of the supported shapes.
func Parse(q string) (Query, bool) {
	s := strings.ToLower(strings.TrimSpace(q))
	s = strings.TrimRight(s, "?.! ")
	s = strings.Join(strings.Fields(s), " ")
	if s == "" || len(s) > 200 {
		return Query{}, false
	}
	switch s {
	case "offline devices", "devices offline", "which devices are offline", "show offline devices", "what is offline":
		return Query{Kind: OfflineDevice}, true
	case "open alerts", "alerts", "show alerts", "show open alerts", "any alerts", "unacknowledged alerts", "what is alarming":
		return Query{Kind: OpenAlerts}, true
	}
	for sev := range severities {
		for _, f := range []string{sev + " alerts", "show " + sev + " alerts", "open " + sev + " alerts", "any " + sev + " alerts"} {
			if s == f {
				return Query{Kind: OpenAlerts, Severity: sev}, true
			}
		}
	}
	if m := reAlerts.FindStringSubmatch(s); m != nil {
		return Query{Kind: DeviceAlerts, Severity: m[1], Device: strings.TrimSpace(m[2])}, true
	}
	if m := reTag.FindStringSubmatch(s); m != nil {
		return Query{Kind: TaggedDevices, Tag: m[1]}, true
	}
	if m := reLatest.FindStringSubmatch(s); m != nil && !severities[m[1]] && m[1] != "alerts" && m[1] != "alert" && m[1] != "devices" {
		return Query{Kind: LatestValue, Point: m[1], Device: strings.TrimSpace(m[2])}, true
	}
	return Query{}, false
}

// Describe is how the question was read, in words, so the user can catch a misreading.
func Describe(q Query) string {
	sev := ""
	if q.Severity != "" {
		sev = q.Severity + " "
	}
	switch q.Kind {
	case OpenAlerts:
		return "open " + sev + "alerts, newest first"
	case DeviceAlerts:
		return "open " + sev + "alerts on the device named " + q.Device
	case LatestValue:
		return "the latest reading of point " + q.Point + " on the device named " + q.Device
	case OfflineDevice:
		return "devices with no reading in the last 15 minutes (the same rule as the fleet page's stale count)"
	case TaggedDevices:
		return "devices tagged " + q.Tag
	}
	return ""
}
