package ingestjournal

// This file is candidate evidence preparation ONLY. No runtime caller, claim
// binding, worker or retry permission is added. A flow definition is only part
// of the required configuration: mutable channels/features/context/control
// policy and rule/position stages are deliberately NOT asserted as captured.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
)

const flowSnapshotPolicy = "flow-evidence-only-v1"
const maxFlowSnapshot = 128 << 10

var ErrSnapshot = errors.New("invalid or mismatched candidate flow snapshot")

// FlowEffect is a conservative inventory, not an execution plan: all graph
// nodes are included, even conditionally reachable ones. Index/ID identify a
// definition location, not one runtime effect (split/join may revisit it).
// Every class remains unsafe for automatic replay under this candidate.
type FlowEffect struct {
	Location string
	Class    string
	Target   string
}
type FlowInventory struct {
	Effects      []FlowEffect
	Dependencies []string
}

// InspectFlowDefinition validates the current grammar without calling Exec or
// any runner. Unknown node kinds fail closed, including newly added kinds that
// a future validator accepts before this classification has been reviewed.
func InspectFlowDefinition(d flow.Definition) (FlowInventory, error) {
	if err := flow.Validate(d); err != nil {
		return FlowInventory{}, fmt.Errorf("%w: %v", ErrSnapshot, err)
	}
	out := FlowInventory{Effects: []FlowEffect{}, Dependencies: []string{"flow_run_history"}}
	deps := map[string]bool{"flow_run_history": true}
	addDep := func(s string) {
		if !deps[s] {
			deps[s] = true
			out.Dependencies = append(out.Dependencies, s)
		}
	}
	add := func(location, kind, target string) {
		out.Effects = append(out.Effects, FlowEffect{location, kind, target})
	}
	if d.Graph == nil {
		for i, s := range d.Steps {
			switch s.Type {
			case "condition":
			case "delay":
				addDep("wall_clock_and_delayed_dispatch")
			case "notify":
				add(fmt.Sprintf("step:%d", i), "external_notification", s.ChannelID)
				addDep("notification_channel_policy_and_target")
			default:
				return FlowInventory{}, ErrSnapshot
			}
		}
		return out, nil
	}
	for _, n := range d.Graph.Nodes {
		switch n.Type {
		case "trigger", "condition", "switch", "change", "split", "join", "template", "range", "debug":
		case "delay":
			addDep("wall_clock_and_delayed_dispatch")
		case "rate_limit":
			addDep("process_local_rate_limiter")
		case "function":
			add(fmt.Sprintf("node:%s", n.ID), "sandbox_computation", "")
			addDep("function_feature_and_runner_policy")
		case "http":
			add(fmt.Sprintf("node:%s", n.ID), "external_http", n.URL)
			addDep("http_feature_and_egress_policy")
		case "notify":
			add(fmt.Sprintf("node:%s", n.ID), "external_notification", n.ChannelID)
			addDep("notification_channel_policy_and_target")
		case "context":
			class := "mutable_context_read"
			if n.Mode != "get" {
				class = "context_mutation"
			}
			add(fmt.Sprintf("node:%s", n.ID), class, n.Scope+":"+n.Key)
			addDep("context_values_and_ordering")
		case "control":
			add(fmt.Sprintf("node:%s", n.ID), "control_request_may_auto_dispatch", n.TargetID)
			addDep("control_feature_target_approval_rate_and_pending_policy")
		default:
			return FlowInventory{}, fmt.Errorf("%w: unclassified node %q", ErrSnapshot, n.Type)
		}
	}
	return out, nil
}

type flowSnapshot struct {
	Policy     string          `json:"policy"`
	Envelope   json.RawMessage `json:"envelope"`
	FlowID     string          `json:"flow_id"`
	FlowName   string          `json:"flow_name"`
	VersionID  string          `json:"version_id"`
	Version    int             `json:"version"`
	Definition json.RawMessage `json:"definition"`
}

func strictJSON(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrSnapshot
	}
	return nil
}
func inspectSnapshot(s flowSnapshot, e Envelope) (FlowInventory, error) {
	_, envelope, err := canonical(e)
	if err != nil || s.Policy != flowSnapshotPolicy || !bytes.Equal(s.Envelope, envelope) || s.FlowID == "" || len(s.FlowID) > 256 || s.FlowName == "" || len(s.FlowName) > 256 || s.VersionID == "" || len(s.VersionID) > 256 || s.Version < 1 || len(s.Definition) > 64<<10 {
		return FlowInventory{}, ErrSnapshot
	}
	var d flow.Definition
	if strictJSON(s.Definition, &d) != nil {
		return FlowInventory{}, ErrSnapshot
	}
	trigger := d.Trig()
	if trigger.DeviceID != e.DeviceID || trigger.PointID != e.PointID {
		return FlowInventory{}, ErrSnapshot
	}
	return InspectFlowDefinition(d)
}

// CapturePublishedFlow atomically reads ONE enabled flow's current published
// version, tenant-scoped on BOTH tables. It returns immutable bytes+digest for
// investigation; it does not store these in admission or authorize execution.
// This is NOT a snapshot of the whole ingest pipeline, nor a proof that all
// dependencies remain unchanged or that a trigger's condition matched.
func (j Journal) CapturePublishedFlow(ctx context.Context, e Envelope, flowID string) ([]byte, string, error) {
	_, b, err := canonical(e)
	if err != nil {
		return nil, "", err
	}
	s := flowSnapshot{Policy: flowSnapshotPolicy, Envelope: b, FlowID: flowID}
	err = j.Pool.QueryRow(ctx, `SELECT f.name,v.id,v.version,v.definition FROM flows f JOIN flow_versions v ON v.id=f.published_version_id AND v.flow_id=f.id AND v.tenant_id=f.tenant_id WHERE f.tenant_id=$1 AND f.id=$2 AND f.enabled AND v.status='published'`, e.TenantID, flowID).Scan(&s.FlowName, &s.VersionID, &s.Version, &s.Definition)
	if err != nil {
		return nil, "", err
	}
	if _, err = inspectSnapshot(s, e); err != nil {
		return nil, "", err
	}
	out, err := json.Marshal(s)
	if err != nil || len(out) > maxFlowSnapshot {
		return nil, "", ErrSnapshot
	}
	// Compact RawMessage output is the stored candidate evidence. Check it again
	// so verify and capture use the exact same representation and input binding.
	sum := sha256.Sum256(out)
	digest := hex.EncodeToString(sum[:])
	if _, err = InspectFlowSnapshot(out, digest, e); err != nil {
		return nil, "", err
	}
	return out, digest, nil
}

// InspectFlowSnapshot checks caller-retained evidence, not today's live config.
// A SHA256 is integrity evidence only, NOT a signature, permission or proof of
// authorship. Keep expectedDigest with the trusted original evidence, not an
// untrusted supplied replacement. Returned inventory grants no replay right.
func InspectFlowSnapshot(b []byte, expectedDigest string, e Envelope) (FlowInventory, error) {
	sum := sha256.Sum256(b)
	if len(b) == 0 || len(b) > maxFlowSnapshot || len(expectedDigest) != 64 || hex.EncodeToString(sum[:]) != expectedDigest {
		return FlowInventory{}, ErrSnapshot
	}
	var s flowSnapshot
	if strictJSON(b, &s) != nil {
		return FlowInventory{}, ErrSnapshot
	}
	return inspectSnapshot(s, e)
}
