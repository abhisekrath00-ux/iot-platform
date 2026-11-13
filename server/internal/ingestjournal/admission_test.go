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
