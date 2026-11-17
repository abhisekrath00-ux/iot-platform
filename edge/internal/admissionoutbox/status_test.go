package admissionoutbox

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/abhisekrath00-ux/iot-platform/edge/internal/driver"
	"strings"
	"testing"
)

func TestRecoveryStatusRealCollectorSession(t *testing.T) {
	cfg := sessionConfig(t)
	cfg.Store.Capacity = 2
	s := sessionOpen(t, cfg)
	existing := reading()
	existing.TenantID = s.tenant
	existing.GatewayID = s.gateway
	mustPut(t, s.store, existing)
	polls := 0
	c, e := NewCollector(collectorConfig(s, pollFunc(func(context.Context) ([]driver.Reading, error) { polls++; return sourceSamples(), nil })))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.PollOnce(t.Context()); !errors.Is(e, ErrCapacity) {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		d, e := c.RecoveryStatus(t.Context())
		if e != nil || d.Collection.Pending != 2 || d.Collection.Last.Retained != 0 || d.Session.Capacity.Retained != 1 || d.Session.LastSend != nil || d.AsOf.IsZero() || polls != 1 {
			t.Fatal(d, e, polls)
		}
		b, _ := json.Marshal(d)
		for _, secret := range []string{cfg.Store.Path, cfg.HTTPS.Endpoint, "private_key", "payload", "input_sha256", existing.EventID} {
			if strings.Contains(string(b), secret) {
				t.Fatal("private evidence exposed", secret)
			}
		}
	}
	if e = c.Stop(t.Context()); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(t.Context()); e != nil {
		t.Fatal(e)
	}
	d, e := c.RecoveryStatus(t.Context())
	if e != nil || d.Collection.State != "stopped" || d.Collection.Pending != 2 || d.Session.State != "closed" || d.Session.Capacity != nil || polls != 1 {
		t.Fatal(d, e, polls)
	}
}
func TestRecoveryStatusNoStaleEvidenceOnFailure(t *testing.T) {
	s := sessionOpen(t, sessionConfig(t))
	c, e := NewCollector(CollectorConfig{Enabled: true, Session: s, Reader: pollFunc(func(context.Context) ([]driver.Reading, error) { t.Fatal("status polled"); return nil, nil }), DeviceID: "d", Points: map[string]string{"p": "V"}})
	if e != nil {
		t.Fatal(e)
	}
	for _, ctx := range []context.Context{nil, func() context.Context { ctx, cancel := context.WithCancel(t.Context()); cancel(); return ctx }()} {
		d, e := c.RecoveryStatus(ctx)
		if e == nil || !d.AsOf.IsZero() || d.Session.Capacity != nil {
			t.Fatal(d, e)
		}
	}
	if e = s.store.Close(); e != nil {
		t.Fatal(e)
	}
	if d, e := c.RecoveryStatus(t.Context()); e == nil || !d.AsOf.IsZero() || d.Session.Capacity != nil {
		t.Fatal(d, e)
	}
	var absent *Collector
	if _, e = absent.RecoveryStatus(t.Context()); !errors.Is(e, ErrSessionClosed) {
		t.Fatal(e)
	}
}
