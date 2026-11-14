package ingestprocessing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/rules"
	"github.com/jackc/pgx/v5"
)

// Plan contains the entire bounded selection, including disabled channels,
// feature policy, unmatched enabled definitions and explicit unresolved stages.
// It is not a promise every flow/rule kind is implemented. Blocked is a durable
// planning quarantine; even otherwise supported effects must not run while any
// stage is blocked. No executor exists yet. Current authorization/revocation
// MUST still be checked before any future effect, without retargeting it.
type Plan struct {
	Policy      string    `json:"policy"`
	Envelope    Envelope  `json:"envelope"`
	SelectedAt  time.Time `json:"selected_at"`
	Device      Device    `json:"device"`
	Maintenance bool      `json:"maintenance"`
	Features    []Feature `json:"features"`
	Rules       []Rule    `json:"rules"`
	Flows       []Flow    `json:"flows"`
	Channels    []Channel `json:"channels"`
	Intents     []Intent  `json:"intents"`
	Blocked     []string  `json:"blocked"`
}
type Device struct {
	Profile  string `json:"profile"`
	LatPoint string `json:"lat_point"`
	LonPoint string `json:"lon_point"`
}
type Feature struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}
type Rule struct {
	ID         string          `json:"id"`
	Version    int             `json:"version"`
	Definition json.RawMessage `json:"definition"`
}
type Flow struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	VersionID  string          `json:"version_id"`
	Version    int             `json:"version"`
	Definition json.RawMessage `json:"definition"`
}
type Channel struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Target  string `json:"target"`
	Enabled bool   `json:"enabled"`
}

// Intent is an occurrence, not a delivered alert. Sequence identifies repeated
// visits to a notify node independently; full input identity is in the parent
// record. Future execution must journal one attempt per occurrence BEFORE I/O.
type Intent struct {
	Sequence    int    `json:"sequence"`
	Source      string `json:"source"`
	SourceID    string `json:"source_id"`
	ChannelID   string `json:"channel_id"`
	ChannelType string `json:"channel_type"`
	Target      string `json:"target"`
	Message     string `json:"message"`
	Severity    string `json:"severity,omitempty"`
	DelayNS     int64  `json:"delay_ns"`
}

func strict(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrPlan
	}
	return nil
}
func decodePlan(b, digest []byte, e Envelope) (Plan, error) {
	sum := sha256.Sum256(b)
	if len(b) == 0 || len(b) > maxPlanBytes || !bytes.Equal(sum[:], digest) {
		return Plan{}, ErrPlan
	}
	var p Plan
	if strict(b, &p) != nil || p.Policy != policy || p.SelectedAt.IsZero() {
		return Plan{}, ErrPlan
	}
	_, want, err := canonical(e)
	if err != nil {
		return Plan{}, err
	}
	_, got, err := canonical(p.Envelope)
	if err != nil || !bytes.Equal(want, got) {
		return Plan{}, ErrPlan
	}
	// Re-derive the supported intents and quarantine from retained configuration,
	// never trust stored intent text alone or consult today's mutable policy.
	expected := p
	expected.Intents = nil
	expected.Blocked = nil
	if err = derive(&expected); err != nil {
		return Plan{}, err
	}
	a, _ := json.Marshal(expected.Intents)
	c, _ := json.Marshal(p.Intents)
	d, _ := json.Marshal(expected.Blocked)
	f, _ := json.Marshal(p.Blocked)
	if !bytes.Equal(a, c) || !bytes.Equal(d, f) {
		return Plan{}, ErrPlan
	}
	return p, nil
}
func capture(ctx context.Context, tx pgx.Tx, e Envelope) (Plan, error) {
	p := Plan{Policy: policy, Envelope: e}
	if err := tx.QueryRow(ctx, `SELECT transaction_timestamp(),profile,COALESCE(lat_point,''),COALESCE(lon_point,'') FROM devices WHERE id=$1 AND tenant_id=$2 AND gateway_id=$3`, e.DeviceID, e.TenantID, e.GatewayID).Scan(&p.SelectedAt, &p.Device.Profile, &p.Device.LatPoint, &p.Device.LonPoint); err != nil {
		return p, err
	}
	// Capture the maintenance decision in the same snapshot with bounded ancestry.
	if err := tx.QueryRow(ctx, `WITH RECURSIVE up AS (
 SELECT a.id,a.parent_id,1 depth FROM assets a JOIN devices d ON d.asset_id=a.id AND d.tenant_id=a.tenant_id WHERE d.id=$2 AND d.tenant_id=$1
 UNION ALL SELECT a.id,a.parent_id,up.depth+1 FROM assets a JOIN up ON a.id=up.parent_id WHERE a.tenant_id=$1 AND up.depth<10)
 SELECT EXISTS(SELECT 1 FROM maintenance_windows w WHERE w.tenant_id=$1 AND w.ended_at IS NULL AND transaction_timestamp()>=w.starts_at AND transaction_timestamp()<w.ends_at AND (w.device_id=$2 OR w.asset_id IN(SELECT id FROM up)))`, e.TenantID, e.DeviceID).Scan(&p.Maintenance); err != nil {
		return p, err
	}
	r, err := tx.Query(ctx, `SELECT feature,enabled FROM tenant_features WHERE tenant_id=$1 ORDER BY feature LIMIT 65`, e.TenantID)
	if err != nil {
		return p, err
	}
	for r.Next() {
		var a Feature
		if err = r.Scan(&a.Name, &a.Enabled); err != nil {
			r.Close()
			return p, err
		}
		p.Features = append(p.Features, a)
	}
	err = r.Err()
	r.Close()
	if err != nil {
		return p, err
	}
	if len(p.Features) > 64 {
		return p, ErrPlan
	}
	r, err = tx.Query(ctx, `SELECT id,version,CASE WHEN octet_length(definition::text)<=65536 THEN definition ELSE NULL END FROM rules WHERE tenant_id=$1 AND enabled ORDER BY id LIMIT 129`, e.TenantID)
	if err != nil {
		return p, err
	}
	for r.Next() {
		var a Rule
		if err = r.Scan(&a.ID, &a.Version, &a.Definition); err != nil {
			r.Close()
			return p, err
		}
		p.Rules = append(p.Rules, a)
	}
	err = r.Err()
	r.Close()
	if err != nil {
		return p, err
	}
	if len(p.Rules) > 128 {
		return p, ErrPlan
	}
	// LEFT JOIN ensures broken pointers, cross-tenant/version/flow bindings and
	// non-published versions quarantine instead of disappearing from selection.
	r, err = tx.Query(ctx, `SELECT f.id,f.name,COALESCE(v.id,''),COALESCE(v.version,0),CASE WHEN v.id IS NULL THEN 'null'::jsonb WHEN octet_length(v.definition::text)<=65536 THEN v.definition ELSE NULL END FROM flows f LEFT JOIN flow_versions v ON v.id=f.published_version_id AND v.flow_id=f.id AND v.tenant_id=f.tenant_id AND v.status='published' WHERE f.tenant_id=$1 AND f.enabled ORDER BY f.id LIMIT 129`, e.TenantID)
	if err != nil {
		return p, err
	}
	for r.Next() {
		var a Flow
		if err = r.Scan(&a.ID, &a.Name, &a.VersionID, &a.Version, &a.Definition); err != nil {
			r.Close()
			return p, err
		}
		p.Flows = append(p.Flows, a)
	}
	err = r.Err()
	r.Close()
	if err != nil {
		return p, err
	}
	if len(p.Flows) > 128 {
		return p, ErrPlan
	}
	r, err = tx.Query(ctx, `SELECT id,type,CASE WHEN octet_length(target)<=8192 THEN target ELSE NULL END,enabled FROM notification_channels WHERE tenant_id=$1 ORDER BY id LIMIT 129`, e.TenantID)
	if err != nil {
		return p, err
	}
	for r.Next() {
		var a Channel
		if err = r.Scan(&a.ID, &a.Type, &a.Target, &a.Enabled); err != nil {
			r.Close()
			return p, err
		}
		p.Channels = append(p.Channels, a)
	}
	err = r.Err()
	r.Close()
	if err != nil {
		return p, err
	}
	if len(p.Channels) > 128 {
		return p, ErrPlan
	}
	if err = derive(&p); err != nil {
		return p, err
	}
	return p, nil
}
func derive(p *Plan) error {
	if len(p.Rules) > 128 || len(p.Flows) > 128 || len(p.Channels) > 128 || len(p.Features) > 64 {
		return ErrPlan
	}
	block := func(source string) { p.Blocked = append(p.Blocked, source) }
	e := p.Envelope
	if e.PointID == p.Device.LatPoint || e.PointID == p.Device.LonPoint {
		block("position:transactional_axis_and_trail_stage_required")
	}
	channels := map[string]Channel{}
	for _, c := range p.Channels {
		if c.ID == "" || len(c.ID) > 256 || len(c.Target) > 8192 || channels[c.ID].ID != "" {
			return ErrPlan
		}
		channels[c.ID] = c
	}
	add := func(source, id, channel, msg, severity string, delay time.Duration) {
		c, ok := channels[channel]
		if !ok || !c.Enabled {
			block(source + ":" + id + ":channel_unavailable:" + channel)
			return
		}
		switch c.Type {
		case "email", "slack", "teams", "sms", "whatsapp", "webhook":
		default:
			block(source + ":" + id + ":channel_adapter_required:" + c.Type)
			return
		}
		p.Intents = append(p.Intents, Intent{len(p.Intents), source, id, c.ID, c.Type, c.Target, msg, severity, int64(delay)})
	}
	for _, r := range p.Rules {
		if r.ID == "" || r.Version < 1 || (len(r.Definition) == 0 || len(r.Definition) > 65536) {
			return ErrPlan
		}
		var d rules.Definition
		if strict(r.Definition, &d) != nil || d.Validate() != nil {
			block("rule:" + r.ID + ":invalid_definition")
			continue
		}
		if d.Kind == "kpi_band" || d.Kind == "forecast_limit" {
			continue
		}
		if d.PointID != e.PointID || (d.DeviceID != "" && d.DeviceID != e.DeviceID) || (d.Profile != "" && d.Profile != p.Device.Profile) {
			continue
		}
		if d.GroupID != "" || d.AssetID != "" || d.Relation != "" || (d.Kind != "" && d.Kind != "threshold") {
			block("rule:" + r.ID + ":scoped_or_history_stage_required")
			continue
		}
		if !((d.Op == ">" && e.Value > d.Threshold) || (d.Op == "<" && e.Value < d.Threshold)) {
			continue
		}
		// Alert insertion/dedupe and shelving must be committed transactionally with
		// progress before notification. Never call the old fire/dispatch functions.
		block("rule:" + r.ID + ":transactional_alert_stage_required")
		if p.Maintenance && rules.Shelvable(d.Severity) {
			continue
		}
		msg := d.Message
		if msg == "" {
			msg = fmt.Sprintf("%s/%s %s %v (value %.3g)", e.DeviceID, e.PointID, d.Op, d.Threshold, e.Value)
		}
		for _, c := range p.Channels {
			if c.Enabled {
				add("rule", r.ID, c.ID, msg, d.Severity, 0)
			}
		}
	}
	for _, f := range p.Flows {
		if f.ID == "" || (len(f.Definition) == 0 || len(f.Definition) > 65536) {
			return ErrPlan
		}
		var d flow.Definition
		if f.VersionID == "" || f.Version < 1 || strict(f.Definition, &d) != nil || bytes.Equal(bytes.TrimSpace(f.Definition), []byte("null")) || flow.Validate(d) != nil {
			block("flow:" + f.ID + ":invalid_published_binding_or_definition")
			continue
		}
		if d.IsScheduled() {
			continue
		}
		trig := d.Trig()
		if trig.DeviceID != e.DeviceID || trig.PointID != e.PointID {
			continue
		}
		if d.Latch || d.CooldownSeconds > 0 {
			block("flow:" + f.ID + ":ordered_history_stage_required")
			continue
		}
		safe := true
		if d.Graph != nil {
			for _, n := range d.Graph.Nodes {
				switch n.Type {
				case "trigger", "condition", "switch", "change", "split", "join", "template", "range", "debug", "delay", "notify":
				default:
					safe = false
					block("flow:" + f.ID + ":node_stage_required:" + n.Type)
				}
			}
		}
		if !safe {
			continue
		}
		// Only the fully classified stateless subset may enter Exec; HTTP, context,
		// control, function and rate-limit never receive a runner or execute here.
		result := d.Exec(e.Value, e.DeviceID, e.PointID, flow.ExecOptions{})
		if len(result.Debug) > 0 {
			block("flow:" + f.ID + ":debug_or_evaluation_detail_requires_review")
		}
		if !result.Matched {
			continue
		}
		for _, a := range result.Actions {
			add("flow", f.ID, a.ChannelID, a.Message, "", a.Delay)
		}
	}
	if len(p.Intents) > 1024 || len(p.Blocked) > 1024 {
		return ErrPlan
	}
	return nil
}
