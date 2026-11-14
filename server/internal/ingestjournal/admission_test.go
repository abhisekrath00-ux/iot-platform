package ingestjournal

import (
	"math"
	"testing"
	"time"
)

func TestCanonicalEnvelope(t *testing.T) {
	base := Envelope{EventID: "e", TenantID: "t", GatewayID: "g", DeviceID: "d", PointID: "p", ObservedAt: time.Date(2026, 11, 14, 0, 0, 0, 123456789, time.UTC), Value: 42, Unit: "C", Quality: "measured", SchemaVersion: 1}
	got, _, err := canonical(base)
	if err != nil || got.ObservedAt.Nanosecond() != 123456000 {
		t.Fatal(got, err)
	}
	for name, change := range map[string]func(*Envelope){"id": func(e *Envelope) { e.EventID = " " }, "time": func(e *Envelope) { e.ObservedAt = time.Time{} }, "nan": func(e *Envelope) { e.Value = math.NaN() }, "infinite": func(e *Envelope) { e.Value = math.Inf(1) }, "quality": func(e *Envelope) { e.Quality = "unknown" }, "version": func(e *Envelope) { e.SchemaVersion = 2 }} {
		t.Run(name, func(t *testing.T) {
			e := base
			change(&e)
			if _, _, err := canonical(e); err == nil {
				t.Fatal("invalid accepted")
			}
		})
	}
}

func TestWorkEnvelopeAndStateValidation(t *testing.T) {
	e := Envelope{EventID: "e", TenantID: "t", GatewayID: "g", DeviceID: "d", PointID: "p", ObservedAt: time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC), Value: 42, Quality: "measured", SchemaVersion: 1}
	_, b, err := canonical(e)
	if err != nil {
		t.Fatal(err)
	}
	const attempt = "f7a77a97-eefc-4a27-aee3-36e8ed61ee12"
	for _, state := range []string{"executing", "completed", "outcome_unknown"} {
		if _, err := decodeWork(b, "t", "e", e.ObservedAt, state, attempt); err != nil {
			t.Fatal(state, err)
		}
	}
	for _, test := range []struct{ tenant, event, state, attempt string }{
		{"foreign", "e", "pending", ""}, {"t", "other", "pending", ""}, {"t", "e", "pending", attempt}, {"t", "e", "executing", ""}, {"t", "e", "completed", "invalid"}, {"t", "e", "retry", ""},
	} {
		if _, err := decodeWork(b, test.tenant, test.event, e.ObservedAt, test.state, test.attempt); err == nil {
			t.Fatal("invalid state/envelope accepted", test)
		}
	}
	if _, err := decodeWork([]byte("{}"), "t", "e", e.ObservedAt, "pending", ""); err == nil {
		t.Fatal("corrupt envelope accepted")
	}
	if _, err := decodeWork(b, "t", "e", e.ObservedAt.Add(time.Second), "pending", ""); err == nil {
		t.Fatal("wrong timestamp accepted")
	}
}
