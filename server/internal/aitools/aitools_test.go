package aitools

import (
	"strings"
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/assistant"
)

func TestResolveBuildsRequestsTheModelCannotShape(t *testing.T) {
	c, err := Resolve("get_device_health", `{"device_id":"pump-1"}`)
	if err != nil || c.Method != "GET" || c.Path != "/v1/devices/pump-1/health" {
		t.Fatalf("%+v %v", c, err)
	}
	c, err = Resolve("get_telemetry", `{"device_id":"d","point_id":"temp","hours":6}`)
	if err != nil || c.Query["hours"] != "6" || c.Query["device_id"] != "d" {
		t.Fatalf("%+v %v", c, err)
	}
	c, err = Resolve("comment_on_alert", `{"alert_id":"a1","body":"checked, valve closed"}`)
	if err != nil || c.Method != "POST" || c.Path != "/v1/alerts/a1/comments" || !strings.Contains(c.Body, "valve closed") || !strings.Contains(c.Impact, "a1") {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestResolveRefusesBadArguments(t *testing.T) {
	bad := map[string][2]string{
		"unknown tool":        {"delete_everything", `{}`},
		"unknown argument":    {"get_device_health", `{"device_id":"a","path":"/v1/users"}`},
		"missing required":    {"get_device_health", `{}`},
		"path traversal":      {"get_device_health", `{"device_id":"../users"}`},
		"slash in id":         {"get_alert", `{"alert_id":"a/ack"}`},
		"query in id":         {"get_alert", `{"alert_id":"a?x=1"}`},
		"wrong type":          {"get_telemetry", `{"device_id":"d","point_id":"p","hours":"6"}`},
		"fraction":            {"get_telemetry", `{"device_id":"d","point_id":"p","hours":1.5}`},
		"out of range":        {"get_telemetry", `{"device_id":"d","point_id":"p","hours":100000}`},
		"enum":                {"list_alerts", `{"status":"all; drop"}`},
		"not an object":       {"fleet_summary", `[1]`},
		"too long":            {"comment_on_alert", `{"alert_id":"a","body":"` + strings.Repeat("x", 1001) + `"}`},
		"empty required text": {"comment_on_alert", `{"alert_id":"a","body":""}`},
	}
	for name, a := range bad {
		if c, err := Resolve(a[0], a[1]); err == nil {
			t.Errorf("%s: accepted %+v", name, c)
		}
	}
}

// The registry must never offer something the platform's own policy would refuse, and a tool's
// declared risk must agree with what the policy does with its request. This keeps the two lists
// from drifting apart.
func TestRegistryAgreesWithPolicy(t *testing.T) {
	for _, n := range Names() {
		tool, _ := Lookup(n)
		path := tool.Path
		for _, p := range tool.Params {
			path = strings.ReplaceAll(path, "{"+p.Name+"}", "x1")
		}
		v, why := assistant.Classify(tool.Method, path)
		if n == "offer_report_download" {
			// File downloads stay denied for the generic api_request tool, so the model can never read
			// file content. This tool is handled by the server (offerReportDownload), which checks
			// the report and hands the user a download button, and never returns the body.
			if v != assistant.Denied {
				t.Errorf("%s: the policy must keep denying report downloads to the model (got %v)", n, v)
			}
			continue
		}
		switch tool.Risk {
		case Read:
			if v != assistant.Read {
				t.Errorf("%s is READ but the policy says %v (%s)", n, v, why)
			}
		default:
			if v != assistant.Confirm {
				t.Errorf("%s is %s but the policy says %v (%s)", n, tool.Risk, v, why)
			}
			if tool.Risk == LowRiskWrite && !assistant.LowRisk(tool.Method, path) {
				t.Errorf("%s is LOW_RISK_WRITE but the policy does not list it as low risk", n)
			}
			if tool.Impact == "" {
				t.Errorf("%s writes but has no impact statement", n)
			}
		}
	}
}

func TestNoToolCanReachForbiddenAreas(t *testing.T) {
	for _, n := range Names() {
		tool, _ := Lookup(n)
		for _, bad := range []string{"/v1/users", "/v1/secrets", "/v1/api-keys", "/v1/ai", "/v1/assistant", "/v1/me", "/v1/commands/"} {
			if strings.HasPrefix(tool.Path, bad) && tool.Method != "GET" {
				t.Errorf("%s writes under %s", n, bad)
			}
		}
		if strings.Contains(tool.Path, "approve") || strings.Contains(tool.Path, "control-targets") {
			t.Errorf("%s touches approval or control targets", n)
		}
	}
}

func TestSpecsAreStrictSchemas(t *testing.T) {
	for _, s := range Specs() {
		if s.Parameters["additionalProperties"] != false {
			t.Errorf("%s allows extra arguments", s.Name)
		}
	}
}
