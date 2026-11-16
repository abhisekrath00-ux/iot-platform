package ingestprocessing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAdmissionReceiptCanonicalBinding(t *testing.T) {
	e := Envelope{EventID: "event", TenantID: "tenant", GatewayID: "gateway", DeviceID: "device", PointID: "point", ObservedAt: time.Date(2026, 11, 16, 0, 0, 0, 123456000, time.UTC), Value: 42, Unit: "C", Quality: "measured", SchemaVersion: 1}
	a := Admission{New: true, State: "pending", Plan: Plan{Policy: policy, Envelope: e}}
	r, err := admissionReceipt(a)
	if err != nil {
		t.Fatal(err)
	}
	// Literal JSON is the canonical protocol vector, not raw wire JSON. This
	// catches accidental field/order/normalization changes without a version bump.
	canonicalJSON := `{"event_id":"event","tenant_id":"tenant","gateway_id":"gateway","device_id":"device","point_id":"point","observed_at":"2026-11-16T00:00:00.123456Z","value":42,"unit":"C","quality":"measured","schema_version":1}`
	sum := sha256.Sum256([]byte(canonicalJSON))
	if r.Version != 1 || r.Policy != policy || r.TenantID != e.TenantID || r.GatewayID != e.GatewayID || r.DeviceID != e.DeviceID || r.PointID != e.PointID || r.EventID != e.EventID || !r.ObservedAt.Equal(e.ObservedAt) || r.InputSHA256 != hex.EncodeToString(sum[:]) || !r.New || r.State != "pending" {
		t.Fatal(r)
	}
	b, _ := json.Marshal(r)
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &fields) != nil || len(fields) != 11 {
		t.Fatal("unexpected disclosure", string(b))
	}
	for _, key := range []string{"version", "policy", "tenant_id", "gateway_id", "device_id", "point_id", "event_id", "observed_at", "input_sha256", "state", "new"} {
		if fields[key] == nil {
			t.Fatal("missing binding", key)
		}
	}
	for _, state := range []string{"pending", "executing", "outcome_unknown", "completed"} {
		a.State, a.New = state, false
		replay, err := admissionReceipt(a)
		if err != nil || replay.InputSHA256 != r.InputSHA256 || replay.State != state || replay.New {
			t.Fatal(replay, err)
		}
	}
	for _, field := range []string{"event", "tenant", "gateway", "device", "point", "time", "value", "unit", "quality"} {
		changed := e
		switch field {
		case "event":
			changed.EventID += "x"
		case "tenant":
			changed.TenantID += "x"
		case "gateway":
			changed.GatewayID += "x"
		case "device":
			changed.DeviceID += "x"
		case "point":
			changed.PointID += "x"
		case "time":
			changed.ObservedAt = changed.ObservedAt.Add(time.Microsecond)
		case "value":
			changed.Value++
		case "unit":
			changed.Unit = "K"
		case "quality":
			changed.Quality = "estimated"
		}
		got, err := admissionReceipt(Admission{State: "pending", Plan: Plan{Policy: policy, Envelope: changed}})
		if err != nil || got.InputSHA256 == r.InputSHA256 {
			t.Fatal(field, got, err)
		}
	}
	for _, mode := range []string{"unknown-state", "new-completed", "policy", "submicrosecond", "nonUTC", "zero"} {
		bad := Admission{State: "pending", Plan: Plan{Policy: policy, Envelope: e}}
		switch mode {
		case "unknown-state":
			bad.State = "delivered"
		case "new-completed":
			bad.State = "completed"
			bad.New = true
		case "policy":
			bad.Plan.Policy = "other"
		case "submicrosecond":
			bad.Plan.Envelope.ObservedAt = e.ObservedAt.Add(time.Nanosecond)
		case "nonUTC":
			bad.Plan.Envelope.ObservedAt = e.ObservedAt.In(time.FixedZone("offset", 3600))
		case "zero":
			bad.Plan.Envelope = Envelope{}
		}
		got, err := admissionReceipt(bad)
		if err == nil || got != (AdmissionReceipt{}) {
			t.Fatal(mode, got, err)
		}
	}
}

func TestAdmissionReceiptPGOriginalIdentityDigest(t *testing.T) {
	x, e := fixture(t)
	b := wirePayload(t, e)
	original, err := x.AdmitMQTT(t.Context(), "t/a/g/g/telemetry", b)
	if err != nil {
		t.Fatal(err)
	}
	_, canonicalBytes, err := canonical(e)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(canonicalBytes)
	if original.InputSHA256 != hex.EncodeToString(sum[:]) || original.TenantID != "a" || original.GatewayID != "g" || original.DeviceID != "d" || original.PointID != "p" {
		t.Fatal(original)
	}
	// Raw whitespace/key ordering, an explicit offset and omitted topic IDs can
	// differ while the exact canonical input is identical. No hashing raw bytes.
	var fields map[string]json.RawMessage
	json.Unmarshal(b, &fields)
	delete(fields, "tenant_id")
	delete(fields, "gateway_id")
	fields["observed_at"], _ = json.Marshal(e.ObservedAt.In(time.FixedZone("offset", 3600)).Format(time.RFC3339Nano))
	alternate, _ := json.MarshalIndent(fields, "", "  ")
	if string(alternate) == string(b) {
		t.Fatal("fixture not alternate wire form")
	}
	exec(t, x.Pool, `UPDATE notification_channels SET target='changed@example.invalid' WHERE id='c'`)
	for _, state := range []string{"pending", "executing", "outcome_unknown", "completed"} {
		if state != "pending" {
			exec(t, x.Pool, `UPDATE ingest_processing_v1 SET state=$1,attempt_id='12345678-1234-1234-1234-123456789012'`, state)
		}
		replay, err := x.AdmitMQTT(t.Context(), "t/a/g/g/telemetry", alternate)
		want := original
		want.New = false
		want.State = state
		if err != nil || replay != want {
			t.Fatal(replay, want, err)
		}
		enc, _ := json.Marshal(replay)
		for _, private := range []string{"synthetic@example.invalid", "changed@example.invalid", "definition", "message", "attempt_id", "plan"} {
			if strings.Contains(string(enc), private) {
				t.Fatal("receipt disclosure", string(enc))
			}
		}
	}
	changed := e
	changed.Value++
	got, err := x.AdmitMQTT(t.Context(), "t/a/g/g/telemetry", wirePayload(t, changed))
	if err == nil || got != (AdmissionReceipt{}) {
		t.Fatal("conflicting value receipt", got, err)
	}
	counts(t, x.Pool, e.EventID, 1)
	historyCount(t, x, 0, "processing_started")
}
