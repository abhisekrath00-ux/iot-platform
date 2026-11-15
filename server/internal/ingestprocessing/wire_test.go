package ingestprocessing

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func wirePayload(t *testing.T, e Envelope) []byte {
	t.Helper()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestWireStrictStableIdentity(t *testing.T) {
	e := Envelope{EventID: "input", TenantID: "a", GatewayID: "g", DeviceID: "d", PointID: "p", ObservedAt: time.Date(2026, 11, 15, 1, 2, 3, 123456000, time.UTC), Value: 0, Quality: "measured", SchemaVersion: 1}
	b := wirePayload(t, e)
	got, err := decodeMQTT("t/a/g/g/telemetry", b)
	if err != nil || got != e {
		t.Fatal(got, err)
	}
	// Missing optional topic identities are bound from the authenticated topic,
	// while missing required value is never silently converted into a zero.
	var fields map[string]json.RawMessage
	json.Unmarshal(b, &fields)
	delete(fields, "tenant_id")
	delete(fields, "gateway_id")
	short, _ := json.Marshal(fields)
	if got, err = decodeMQTT("t/a/g/g/telemetry", short); err != nil || got != e {
		t.Fatal(got, err)
	}
	for _, k := range []string{"event_id", "device_id", "point_id", "observed_at", "value", "quality", "schema_version"} {
		var f map[string]json.RawMessage
		json.Unmarshal(b, &f)
		delete(f, k)
		bad, _ := json.Marshal(f)
		if _, err = decodeMQTT("t/a/g/g/telemetry", bad); !errors.Is(err, ErrWire) {
			t.Fatal("missing required field accepted", k)
		}
	}
	for _, tc := range []struct{ name, topic, body string }{
		{"spoof-tenant", "t/b/g/g/telemetry", string(b)},
		{"spoof-gateway", "t/a/g/other/telemetry", string(b)},
		{"wildcard", "t/+/g/g/telemetry", string(b)},
		{"wrong-topic", "t/a/g/g/cmd/ack", string(b)},
		{"empty-id", "t//g/g/telemetry", string(b)},
		{"newline-topic", "t/a\n/g/g/telemetry", string(b)},
		{"wrong-case", "t/a/g/g/telemetry", strings.Replace(string(b), "event_id", "EVENT_ID", 1)},
		{"duplicate", "t/a/g/g/telemetry", strings.Replace(string(b), `"event_id":"input"`, `"event_id":"wrong","event_id":"input"`, 1)},
		{"null-value", "t/a/g/g/telemetry", strings.Replace(string(b), `"value":0`, `"value":null`, 1)},
		{"submicrosecond", "t/a/g/g/telemetry", strings.Replace(string(b), ".123456Z", ".123456789Z", 1)},
		{"zero-time", "t/a/g/g/telemetry", strings.Replace(string(b), "2026-11-15T01:02:03.123456Z", "0001-01-01T00:00:00Z", 1)},
		{"unknown", "t/a/g/g/telemetry", strings.TrimSuffix(string(b), "}") + `,"command":"send"}`},
		{"trailing", "t/a/g/g/telemetry", string(b) + `{}`},
		{"huge", "t/a/g/g/telemetry", strings.Repeat(" ", 8193)},
		{"invalid-utf8", "t/a/g/g/telemetry", string([]byte{255})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeMQTT(tc.topic, []byte(tc.body)); !errors.Is(err, ErrWire) {
				t.Fatal(err)
			}
		})
	}
}
func TestWireAdmissionPGCommitReplayAndNoExecution(t *testing.T) {
	x, e := fixture(t)
	b := wirePayload(t, e)
	r, err := x.AdmitMQTT(t.Context(), "t/a/g/g/telemetry", b)
	if err != nil || !r.New || r.State != "pending" || r.EventID != e.EventID || !r.ObservedAt.Equal(e.ObservedAt) {
		t.Fatal(r, err)
	}
	counts(t, x.Pool, e.EventID, 1)
	historyCount(t, x, 0, "processing_started")
	exec(t, x.Pool, `UPDATE notification_channels SET target='changed@example.invalid' WHERE id='c'`)
	r, err = x.AdmitMQTT(t.Context(), "t/a/g/g/telemetry", b)
	if err != nil || r.New || r.State != "pending" {
		t.Fatal(r, err)
	}
	rec, err := x.Read(t.Context(), e.TenantID, e.EventID, e.ObservedAt)
	if err != nil || rec.Plan.Intents[0].Target != "synthetic@example.invalid" {
		t.Fatal(rec, err)
	}
	encoded, _ := json.Marshal(r)
	for _, secret := range []string{"target", "message", "definition", "synthetic@example.invalid", "changed@example.invalid"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("receipt disclosure", string(encoded))
		}
	}
	for _, state := range []string{"executing", "outcome_unknown", "completed"} {
		exec(t, x.Pool, `UPDATE ingest_processing_v1 SET state=$1,attempt_id='00000000-0000-0000-0000-000000000001'`, state)
		r, err = x.AdmitMQTT(t.Context(), "t/a/g/g/telemetry", b)
		if err != nil || r.New || r.State != state {
			t.Fatal(r, err)
		}
	}
	historyCount(t, x, 0, "processing_started")
}
func TestWireAdmissionNoReceiptOnFailure(t *testing.T) {
	for _, mode := range []string{"disabled", "canceled", "insert-refusal", "commit-refusal", "conflict", "legacy", "foreign-topic", "missing-schema"} {
		t.Run(mode, func(t *testing.T) {
			x, e := fixture(t)
			switch mode {
			case "disabled":
				x.Enabled = false
			case "insert-refusal":
				exec(t, x.Pool, `CREATE FUNCTION fail_wire() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic receipt refusal';END$$;CREATE TRIGGER fail_wire BEFORE INSERT ON ingest_processing_v1 FOR EACH ROW EXECUTE FUNCTION fail_wire()`)
			case "commit-refusal":
				exec(t, x.Pool, `CREATE FUNCTION fail_wire_commit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic commit refusal';END$$;CREATE CONSTRAINT TRIGGER fail_wire_commit AFTER INSERT ON ingest_processing_v1 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_wire_commit()`)
			case "conflict":
				mustAdmit(t, x, e)
				e.Value++
			case "legacy":
				exec(t, x.Pool, `INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,value,unit,quality,schema_version) VALUES($1,'a','g','d','p',$2,42,'','measured',1)`, e.EventID, e.ObservedAt)
			case "missing-schema":
				exec(t, x.Pool, `DROP TABLE ingest_processing_v1`)
			}
			ctx := t.Context()
			if mode == "canceled" {
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			topic := "t/a/g/g/telemetry"
			if mode == "foreign-topic" {
				topic = "t/b/g/g/telemetry"
			}
			r, err := x.AdmitMQTT(ctx, topic, wirePayload(t, e))
			if err == nil || r != (AdmissionReceipt{}) {
				t.Fatal("false receipt", r, err)
			}
			if mode == "insert-refusal" || mode == "commit-refusal" {
				counts(t, x.Pool, e.EventID, 0)
			}
			historyCount(t, x, 0, "processing_started")
		})
	}
}
func TestWireConcurrentExactAdmissionsPG(t *testing.T) {
	x, e := fixture(t)
	b := wirePayload(t, e)
	var wg sync.WaitGroup
	var mu sync.Mutex
	newCount := 0
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := x.AdmitMQTT(t.Context(), "t/a/g/g/telemetry", b)
			if err == nil {
				mu.Lock()
				if r.New {
					newCount++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if newCount != 1 {
		t.Fatal("multiple initial receipts", newCount)
	}
	// Serializable losers return no receipt; exact explicit reconciliation sees
	// retained input without starting/replaying any effect.
	for range 3 {
		r, err := x.AdmitMQTT(t.Context(), "t/a/g/g/telemetry", b)
		if err != nil || r.New || r.State != "pending" {
			t.Fatal(r, err)
		}
	}
	counts(t, x.Pool, e.EventID, 1)
	historyCount(t, x, 0, "processing_started")
}
