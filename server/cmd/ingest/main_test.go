package main

import (
	"fmt"
	"testing"
	"time"
)

func validPayload() []byte {
	return []byte(`{"event_id":"e1","tenant_id":"acme","gateway_id":"gw1","device_id":"d1","point_id":"temp","observed_at":"` +
		time.Now().UTC().Format(time.RFC3339) + `","value":21.5,"schema_version":1}`)
}

func TestResolveEnvelopeOK(t *testing.T) {
	e, err := resolveEnvelope("t/acme/g/gw1/telemetry", validPayload(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if e.TenantID != "acme" || e.GatewayID != "gw1" || e.Quality != "measured" {
		t.Fatalf("%+v", e)
	}
}

func TestResolveEnvelopeIdentityFromTopic(t *testing.T) {
	// payload omits identity: topic supplies it
	p := []byte(`{"event_id":"e2","device_id":"d1","point_id":"temp","observed_at":"` +
		time.Now().UTC().Format(time.RFC3339) + `","value":1,"schema_version":1}`)
	e, err := resolveEnvelope("t/acme/g/gw1/telemetry", p, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if e.TenantID != "acme" || e.GatewayID != "gw1" {
		t.Fatalf("identity not derived from topic: %+v", e)
	}
}

func TestResolveEnvelopeRejectsTenantSpoof(t *testing.T) {
	p := []byte(`{"event_id":"e3","tenant_id":"victim","gateway_id":"gw1","device_id":"d1","point_id":"temp","observed_at":"` +
		time.Now().UTC().Format(time.RFC3339) + `","value":1,"schema_version":1}`)
	if _, err := resolveEnvelope("t/acme/g/gw1/telemetry", p, time.Now()); err == nil {
		t.Fatal("cross-tenant spoof accepted")
	}
}

func TestResolveEnvelopeRejectsGatewaySpoof(t *testing.T) {
	p := []byte(`{"event_id":"e4","tenant_id":"acme","gateway_id":"other-gw","device_id":"d1","point_id":"temp","observed_at":"` +
		time.Now().UTC().Format(time.RFC3339) + `","value":1,"schema_version":1}`)
	if _, err := resolveEnvelope("t/acme/g/gw1/telemetry", p, time.Now()); err == nil {
		t.Fatal("gateway spoof accepted")
	}
}

func TestResolveEnvelopeRejectsBadTopics(t *testing.T) {
	for _, topic := range []string{"", "t//g/gw1/telemetry", "t/acme/g//telemetry", "x/acme/g/gw1/telemetry", "t/acme/g/gw1/commands", "t/acme/telemetry"} {
		if _, err := resolveEnvelope(topic, validPayload(), time.Now()); err == nil {
			t.Fatalf("topic %q accepted", topic)
		}
	}
}

func TestResolveEnvelopeRejectsBadPayloads(t *testing.T) {
	now := time.Now()
	cases := map[string][]byte{
		"not json":        []byte(`{`),
		"wrong schema":    []byte(`{"event_id":"e","device_id":"d","point_id":"p","observed_at":"` + now.UTC().Format(time.RFC3339) + `","schema_version":2}`),
		"missing event":   []byte(`{"device_id":"d","point_id":"p","observed_at":"` + now.UTC().Format(time.RFC3339) + `","schema_version":1}`),
		"future observed": []byte(`{"event_id":"e","device_id":"d","point_id":"p","observed_at":"` + now.Add(time.Hour).UTC().Format(time.RFC3339) + `","schema_version":1}`),
		"ancient":         []byte(`{"event_id":"e","device_id":"d","point_id":"p","observed_at":"` + now.Add(-40*24*time.Hour).UTC().Format(time.RFC3339) + `","schema_version":1}`),
	}
	for name, p := range cases {
		if _, err := resolveEnvelope("t/acme/g/gw1/telemetry", p, now); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func ExampleresolveEnvelope() {
	e, _ := resolveEnvelope("t/acme/g/gw1/telemetry",
		[]byte(`{"event_id":"e1","device_id":"d1","point_id":"temp","observed_at":"2026-09-28T00:00:00Z","value":21.5,"schema_version":1}`),
		time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC))
	fmt.Println(e.TenantID, e.GatewayID, e.Quality)
	// Output: acme gw1 measured
}

func TestSubTopicShared(t *testing.T) {
	t.Setenv("INGEST_SHARED_GROUP", "")
	if got := subTopic("t/+/g/+/telemetry"); got != "t/+/g/+/telemetry" {
		t.Fatalf("default must be unchanged, got %s", got)
	}
	t.Setenv("INGEST_SHARED_GROUP", "ingest")
	if got := subTopic("t/+/g/+/telemetry"); got != "$share/ingest/t/+/g/+/telemetry" {
		t.Fatalf("shared topic wrong: %s", got)
	}
}

func TestMissingObservedAtIsStampedEstimated(t *testing.T) {
	p := []byte(`{"schema_version":1,"event_id":"e1","device_id":"d","point_id":"p","value":1}`)
	now := time.Now()
	e, err := resolveEnvelope("t/acme/g/gw1/telemetry", p, now)
	if err != nil || e.Quality != "estimated" || !e.ObservedAt.Equal(now) {
		t.Fatalf("%+v %v", e, err)
	}
	// topic identity still wins over a spoofed payload tenant
	p = []byte(`{"schema_version":1,"event_id":"e1","tenant_id":"evil","device_id":"d","point_id":"p","value":1}`)
	if _, err := resolveEnvelope("t/acme/g/gw1/telemetry", p, now); err == nil {
		t.Fatal("spoofed tenant accepted")
	}
}
