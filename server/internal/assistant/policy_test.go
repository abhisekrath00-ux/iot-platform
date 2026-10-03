package assistant

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		method, path string
		want         Verdict
	}{
		{"GET", "/v1/alerts", Read},
		{"GET", "/v1/devices/d1/health", Read},
		{"GET", "/v1/relations/downstream", Read},
		{"POST", "/v1/alerts/a1/ack", Confirm},
		{"POST", "/v1/devices", Confirm},
		{"PUT", "/v1/dashboards/x", Confirm},
		{"POST", "/v1/commands", Confirm}, // raising a request is allowed; approving is not
		{"DELETE", "/v1/assets/a", Confirm},
		// human-only
		{"POST", "/v1/commands/c1/approve", Denied},
		{"POST", "/v1/commands/c1/reject", Denied},
		{"POST", "/v1/control-targets", Denied},
		{"POST", "/v1/control-targets/t/mode", Denied},
		{"POST", "/v1/control-targets/t/enabled", Denied},
		{"PUT", "/v1/features/control_nodes", Denied},
		{"PUT", "/v1/secrets/x", Denied},
		{"GET", "/v1/secrets", Denied},
		{"GET", "/v1/api-keys", Denied},
		{"POST", "/v1/api-keys", Denied},
		{"POST", "/v1/devices/d/tokens", Denied},
		{"GET", "/v1/devices/d/tokens", Denied},
		{"PUT", "/v1/retention", Denied},
		{"POST", "/v1/broker/acl/regenerate", Denied},
		{"POST", "/v1/telemetry/ingest", Denied},
		{"PUT", "/v1/gateways/g/edge-rules", Denied},
		{"POST", "/v1/fleet/campaigns/c/start", Denied},
		{"PUT", "/v1/direct-auth/policy", Denied},
		{"POST", "/v1/enrollment/tokens", Denied},
		{"GET", "/v1/ai/settings", Denied},
		{"PUT", "/v1/ai/settings", Denied},
		{"POST", "/v1/assistant/actions/x/confirm", Denied},
		{"POST", "/v1/users", Denied},
		{"PUT", "/v1/users/u/role", Denied},
		// path tricks
		{"GET", "/v1/alerts/../secrets", Denied},
		{"GET", "/v1//secrets", Denied},
		{"GET", "/v1/alerts?x=1", Denied},
		{"GET", "http://evil/v1/alerts", Denied},
		{"GET", "/healthz", Denied},
		{"GET", "/v1/SECRETS", Read}, // not a real route; the mux 404s it, and it grants nothing
		{"TRACE", "/v1/alerts", Denied},
		{"POST", "/v1/devices/d/tags", Denied}, // wrong method for that route
	}
	for _, c := range cases {
		if got, why := Classify(c.method, c.path); got != c.want {
			t.Errorf("%s %s: got %v (%s), want %v", c.method, c.path, got, why, c.want)
		}
	}
}
