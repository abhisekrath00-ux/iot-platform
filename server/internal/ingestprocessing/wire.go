package ingestprocessing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrWire = errors.New("staged telemetry requires an exact authenticated topic and bounded stable envelope")

// AdmissionReceipt is returned only after Admit reports committed evidence or
// reconciles an identical retained input. It is NOT an MQTT PUBACK, edge receipt,
// work-completion, provider acceptance or permission to execute/retry effects.
// Only the exact identity, retained state and commit/replay disposition escape;
// destinations, definitions, message text and the retained plan stay private.
type AdmissionReceipt struct {
	EventID    string    `json:"event_id"`
	ObservedAt time.Time `json:"observed_at"`
	State      string    `json:"state"`
	New        bool      `json:"new"`
}

// AdmitMQTT is the disabled engine's strict telemetry-wire boundary. The caller
// must already have authenticated the publisher and enforced broker topic ACLs;
// a topic string by itself is NOT proof of that authentication. No production
// startup handler or MQTT ACK mode invokes this method. All-handler persistent
// sessions/manual ACK/backpressure/shutdown remain separate acceptance gates.
//
// There is no receive-time fallback: the publisher must retain the same exact
// event ID and observation timestamp across retries. Nanoseconds not exactly
// representable in PostgreSQL are refused, not rounded into another identity.
// Errors return a ZERO receipt, including uncertain commit outcomes. The caller
// may reconcile the SAME input but must not claim a new admission or delivery.
func (x Engine) AdmitMQTT(ctx context.Context, topic string, payload []byte) (AdmissionReceipt, error) {
	if !x.Enabled {
		return AdmissionReceipt{}, ErrDisabled
	}
	e, err := decodeMQTT(topic, payload)
	if err != nil {
		return AdmissionReceipt{}, err
	}
	a, err := x.Admit(ctx, e)
	if err != nil {
		return AdmissionReceipt{}, err
	}
	// Admit validated this identity against both canonical envelope and plan.
	return AdmissionReceipt{EventID: a.Plan.Envelope.EventID, ObservedAt: a.Plan.Envelope.ObservedAt, State: a.State, New: a.New}, nil
}
func decodeMQTT(topic string, payload []byte) (Envelope, error) {
	parts := strings.Split(topic, "/")
	validID := func(s string) bool {
		return s != "" && len(s) <= 256 && utf8.ValidString(s) && strings.IndexFunc(s, func(r rune) bool { return r <= 32 || r == 127 || r == '+' || r == '#' || r == '/' }) < 0
	}
	if len(parts) != 5 || parts[0] != "t" || parts[2] != "g" || parts[4] != "telemetry" || !validID(parts[1]) || !validID(parts[3]) || len(topic) > 540 || len(payload) == 0 || len(payload) > 8192 || !utf8.Valid(payload) || !uniqueJSON(payload) {
		return Envelope{}, ErrWire
	}
	// Exact literal keys only: encoding/json alone accepts case-folded names and
	// duplicate keys. Never let a later duplicate replace the retained identity.
	var fields map[string]json.RawMessage
	if json.Unmarshal(payload, &fields) != nil || fields == nil {
		return Envelope{}, ErrWire
	}
	allowed := map[string]bool{"event_id": true, "tenant_id": true, "gateway_id": true, "device_id": true, "point_id": true, "observed_at": true, "value": true, "unit": true, "quality": true, "schema_version": true}
	for k, v := range fields {
		if !allowed[k] || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return Envelope{}, ErrWire
		}
	}
	for _, k := range []string{"event_id", "device_id", "point_id", "observed_at", "value", "quality", "schema_version"} {
		if fields[k] == nil {
			return Envelope{}, ErrWire
		}
	}
	var e Envelope
	if strict(payload, &e) != nil || !validID(e.EventID) || !validID(e.DeviceID) || !validID(e.PointID) || !e.ObservedAt.Equal(e.ObservedAt.Truncate(time.Microsecond)) {
		return Envelope{}, ErrWire
	}
	if (fields["tenant_id"] != nil && e.TenantID != parts[1]) || (fields["gateway_id"] != nil && e.GatewayID != parts[3]) {
		return Envelope{}, ErrWire
	}
	e.TenantID, e.GatewayID = parts[1], parts[3]
	normalized, _, err := canonical(e)
	if err != nil {
		return Envelope{}, ErrWire
	}
	return normalized, nil
}
