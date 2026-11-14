package ingestjournal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
)

func snapshotEnvelope() Envelope {
	return Envelope{EventID: "event", TenantID: "tenant", GatewayID: "gw", DeviceID: "device", PointID: "point", ObservedAt: time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC), Value: 42, Quality: "measured", SchemaVersion: 1}
}
func linearDef() flow.Definition {
	return flow.Definition{Trigger: flow.Trigger{DeviceID: "device", PointID: "point", Op: ">", Value: 0}, Steps: []flow.Step{{Type: "notify", ChannelID: "channel", Message: "value {value}"}}}
}
func makeEvidence(t *testing.T, d flow.Definition) ([]byte, string) {
	t.Helper()
	e := snapshotEnvelope()
	_, eb, _ := canonical(e)
	db, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(flowSnapshot{flowSnapshotPolicy, eb, "flow", "Name", "version", 1, db})
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(b)
	return b, hex.EncodeToString(h[:])
}
func graphDef(nodes ...flow.Node) flow.Definition {
	g := &flow.Graph{Nodes: append([]flow.Node{{ID: "start", Type: "trigger", DeviceID: "device", PointID: "point", Op: ">", Value: 0}}, nodes...)}
	if len(nodes) > 0 && nodes[len(nodes)-1].Type != "notify" {
		g.Nodes = append(g.Nodes, flow.Node{ID: "sink", Type: "notify", ChannelID: "sink-channel", Message: "sink"})
	}
	for i := 1; i < len(g.Nodes); i++ {
		g.Edges = append(g.Edges, flow.Edge{From: g.Nodes[i-1].ID, To: g.Nodes[i].ID})
	}
	return flow.Definition{Graph: g}
}
func TestFlowEvidenceBoundToCanonicalInputAndExactBytes(t *testing.T) {
	b, h := makeEvidence(t, linearDef())
	e := snapshotEnvelope()
	inv, err := InspectFlowSnapshot(b, h, e)
	if err != nil || len(inv.Effects) != 1 || inv.Effects[0].Class != "external_notification" {
		t.Fatal(inv, err)
	}
	for name, mutate := range map[string]func(*Envelope){"tenant": func(e *Envelope) { e.TenantID = "other" }, "event": func(e *Envelope) { e.EventID = "other" }, "time": func(e *Envelope) { e.ObservedAt = e.ObservedAt.Add(time.Microsecond) }, "value": func(e *Envelope) { e.Value++ }, "device": func(e *Envelope) { e.DeviceID = "other" }, "point": func(e *Envelope) { e.PointID = "other" }} {
		t.Run(name, func(t *testing.T) {
			x := e
			mutate(&x)
			if _, err := InspectFlowSnapshot(b, h, x); err == nil {
				t.Fatal("input substitution accepted")
			}
		})
	}
	same := e
	same.ObservedAt = e.ObservedAt.In(time.FixedZone("offset", 19800)).Add(10 * time.Nanosecond)
	if _, err := InspectFlowSnapshot(b, h, same); err != nil {
		t.Fatal("canonical equivalent", err)
	}
	changed := append([]byte{}, b...)
	changed[len(changed)-2] ^= 1
	for _, x := range [][]byte{nil, changed, append(append([]byte{}, b...), []byte(" {}")...)} {
		if _, err := InspectFlowSnapshot(x, h, e); err == nil {
			t.Fatal("changed bytes accepted")
		}
	}
	if _, err := InspectFlowSnapshot(b, "", e); err == nil {
		t.Fatal("missing trusted digest")
	}
}
func TestFlowEvidenceRejectsUnknownFieldsPolicyAndInvalidVersions(t *testing.T) {
	b, _ := makeEvidence(t, linearDef())
	var s flowSnapshot
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*flowSnapshot){"policy": func(s *flowSnapshot) { s.Policy = "future" }, "version": func(s *flowSnapshot) { s.Version = 0 }, "emptyversion": func(s *flowSnapshot) { s.VersionID = "" }, "unknownfield": func(s *flowSnapshot) { s.Definition = json.RawMessage(`{"future_effect":true}`) }, "unknownnode": func(s *flowSnapshot) {
		d := graphDef(flow.Node{ID: "new", Type: "future_effect"})
		s.Definition, _ = json.Marshal(d)
	}} {
		t.Run(name, func(t *testing.T) {
			x := s
			change(&x)
			raw, _ := json.Marshal(x)
			sum := sha256.Sum256(raw)
			if _, err := InspectFlowSnapshot(raw, hex.EncodeToString(sum[:]), snapshotEnvelope()); err == nil {
				t.Fatal("unsupported snapshot accepted")
			}
		})
	}
}
func TestFlowInventoryIncludesConditionalEffectsWithoutExecutingThem(t *testing.T) {
	cases := []struct {
		node       flow.Node
		class, dep string
	}{
		{flow.Node{ID: "n", Type: "notify", ChannelID: "ch", Message: "x"}, "external_notification", "notification_channel_policy_and_target"},
		{flow.Node{ID: "n", Type: "http", Method: "GET", URL: "https://example.invalid/value", Target: "v"}, "external_http", "http_feature_and_egress_policy"},
		{flow.Node{ID: "n", Type: "http", Method: "POST", URL: "https://example.invalid/value", Target: "v"}, "external_http", "http_feature_and_egress_policy"},
		{flow.Node{ID: "n", Type: "control", TargetID: "alarm"}, "control_request_may_auto_dispatch", "control_feature_target_approval_rate_and_pending_policy"},
		{flow.Node{ID: "n", Type: "context", Mode: "get", Scope: "global", Key: "total", Target: "v"}, "mutable_context_read", "context_values_and_ordering"},
		{flow.Node{ID: "n", Type: "context", Mode: "set", Scope: "flow", Key: "total"}, "context_mutation", "context_values_and_ordering"},
		{flow.Node{ID: "n", Type: "context", Mode: "incr", Scope: "flow", Key: "total"}, "context_mutation", "context_values_and_ordering"},
		{flow.Node{ID: "n", Type: "function", Code: "return msg;"}, "sandbox_computation", "function_feature_and_runner_policy"},
	}
	for _, c := range cases {
		t.Run(c.node.Type+c.node.Mode+c.node.Method, func(t *testing.T) {
			d := graphDef(c.node)
			inv, err := InspectFlowDefinition(d)
			if err != nil {
				t.Fatal(err)
			}
			if len(inv.Effects) < 1 || !reflect.DeepEqual(inv.Effects[:1], []FlowEffect{{"node:n", c.class, func() string {
				switch c.node.Type {
				case "notify":
					return "ch"
				case "http":
					return c.node.URL
				case "control":
					return "alarm"
				case "context":
					return c.node.Scope + ":total"
				}
				return ""
			}()}}) {
				t.Fatal(inv)
			}
			if len(inv.Dependencies) < 2 || !reflect.DeepEqual(inv.Dependencies[:2], []string{"flow_run_history", c.dep}) {
				t.Fatal(inv.Dependencies)
			}
			// The evidence cannot contain a runner/client and classification never calls Exec.
			b, h := makeEvidence(t, d)
			if _, err := InspectFlowSnapshot(b, h, snapshotEnvelope()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestPureNodesStillExposeHistoryClockAndProcessLocalDependencies(t *testing.T) {
	for _, node := range []flow.Node{{ID: "n", Type: "delay", Seconds: 1}, {ID: "n", Type: "rate_limit", Seconds: 1}} {
		inv, err := InspectFlowDefinition(graphDef(node))
		if err != nil || len(inv.Effects) != 1 || len(inv.Dependencies) != 3 {
			t.Fatal(fmt.Sprint(node), inv, err)
		}
	}
	d := linearDef()
	d.Steps = append([]flow.Step{{Type: "delay", Seconds: 1}}, d.Steps...)
	inv, err := InspectFlowDefinition(d)
	if err != nil || len(inv.Dependencies) != 3 || inv.Effects[0].Location != "step:1" {
		t.Fatal(inv, err)
	}
}
