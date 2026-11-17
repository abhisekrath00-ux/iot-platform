package admissionoutbox

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func sessionConfig(t *testing.T) SessionConfig {
	t.Helper()
	f := mtlsFixture(t)
	c, _ := trust(t)
	return SessionConfig{Enabled: true, Store: config(t, 1), HTTPS: HTTPSConfig{Enabled: true, Endpoint: "https://127.0.0.1:1/staged/processing-v1/admit", RootCAs: f.roots, ClientCertificate: f.client, Timeout: time.Second}, Trust: c, BatchLimit: 1}
}
func sessionOpen(t *testing.T, c SessionConfig) *Session {
	t.Helper()
	s, e := OpenSession(t.Context(), c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close(context.Background()) })
	return s
}
func TestSessionConfigRefusesBeforeFile(t *testing.T) {
	for _, mode := range []string{"disabled", "store-disabled", "https-disabled", "trust-disabled", "trust-key", "trust-route", "trust-audience", "batch", "endpoint", "context"} {
		t.Run(mode, func(t *testing.T) {
			c := sessionConfig(t)
			ctx := t.Context()
			switch mode {
			case "disabled":
				c.Enabled = false
			case "store-disabled":
				c.Store.Enabled = false
			case "https-disabled":
				c.HTTPS.Enabled = false
			case "trust-disabled":
				c.Trust.Enabled = false
			case "trust-key":
				c.Trust.PublicKey = nil
			case "trust-route":
				c.Trust.TenantID = "+"
			case "trust-audience":
				c.Trust.Audience = "with space"
			case "batch":
				c.BatchLimit = 0
			case "endpoint":
				c.HTTPS.Endpoint = "http://localhost/admit"
			case "context":
				ctx = nil
			}
			if s, e := OpenSession(ctx, c); s != nil || e == nil {
				t.Fatal("invalid session opened")
			}
			if _, e := os.Stat(c.Store.Path); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("invalid config touched database", e)
			}
		})
	}
}
func TestSessionRetainCapacityScopeAndReopen(t *testing.T) {
	cfg := sessionConfig(t)
	s := sessionOpen(t, cfg)
	r := reading()
	it, e := s.Retain(t.Context(), r)
	if e != nil {
		t.Fatal(e)
	}
	status, e := s.Status(t.Context())
	if e != nil || status.State != "open" || status.Capacity == nil || status.Capacity.Capacity != 1 || !status.Capacity.Full || status.LastSend != nil {
		t.Fatal(status, e)
	}
	if again, e := s.Retain(t.Context(), r); e != nil || again.Sequence != it.Sequence {
		t.Fatal(again, e)
	}
	second := r
	second.EventID = "second"
	if got, e := s.Retain(t.Context(), second); !errors.Is(e, ErrCapacity) || got.Sequence != 0 {
		t.Fatal(got, e)
	}
	foreign := r
	foreign.TenantID = "foreign"
	if got, e := s.Retain(t.Context(), foreign); !errors.Is(e, ErrSignature) || got.Sequence != 0 {
		t.Fatal(got, e)
	}
	changed := r
	changed.Value++
	if got, e := s.Retain(t.Context(), changed); !errors.Is(e, ErrConflict) || got.Sequence != 0 {
		t.Fatal(got, e)
	}
	if e = s.Close(t.Context()); e != nil {
		t.Fatal(e)
	}
	if got, e := s.Retain(t.Context(), r); !errors.Is(e, ErrSessionClosed) || got.Sequence != 0 {
		t.Fatal(got, e)
	}
	if result, e := s.SendOnce(t.Context()); !errors.Is(e, ErrSessionClosed) || result.Attempted != 0 {
		t.Fatal(result, e)
	}
	status, e = s.Status(t.Context())
	if e != nil || status.State != "closed" || status.Capacity != nil {
		t.Fatal(status, e)
	}
	reopened := sessionOpen(t, cfg)
	rows, e := reopened.store.Next(t.Context(), 1)
	if e != nil || len(rows) != 1 || rows[0].InputSHA256 != it.InputSHA256 {
		t.Fatal(rows, e)
	}
	status, e = reopened.Status(t.Context())
	if e != nil || status.LastSend != nil || !status.Capacity.Full {
		t.Fatal(status, e)
	}
}
func TestSessionSendOutcomeAndDrain(t *testing.T) {
	cfg := sessionConfig(t)
	s := sessionOpen(t, cfg)
	result, e := s.SendOnce(t.Context())
	if e != nil || result.Attempted != 0 {
		t.Fatal(result, e)
	}
	status, e := s.Status(t.Context())
	if e != nil || status.LastSend == nil || status.LastSend.Outcome != "idle" {
		t.Fatal(status, e)
	}
	s.Retain(t.Context(), reading())
	// Only this isolated test substitutes the transport with a blocked exchange.
	entered := make(chan struct{})
	finish := make(chan struct{})
	done := make(chan error, 1)
	s.publisher.exchange = exchangeFunc(func(context.Context, Item) ([]byte, error) {
		close(entered)
		<-finish
		return nil, errors.New("uncertain test response")
	})
	go func() { _, e := s.SendOnce(t.Context()); done <- e }()
	<-entered
	if result, e := s.SendOnce(t.Context()); !errors.Is(e, ErrPublisherBusy) || result.Attempted != 0 {
		t.Fatal(result, e)
	}
	status, e = s.Status(t.Context())
	if e != nil || status.LastSend.Outcome != "idle" || !status.Sending {
		t.Fatal("busy overwrote attempt", status, e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if e = s.Close(ctx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	status, e = s.Status(t.Context())
	if e != nil || status.State != "closing" || status.Capacity != nil || !status.Sending {
		t.Fatal(status, e)
	}
	if _, e = s.Retain(t.Context(), reading()); !errors.Is(e, ErrSessionClosed) {
		t.Fatal(e)
	}
	if _, e = s.store.acquireOwnership(t.Context()); !errors.Is(e, ErrOwnership) {
		t.Fatal("close timeout dropped ownership", e)
	}
	close(finish)
	if e = <-done; e == nil {
		t.Fatal("missing uncertainty")
	}
	if e = s.Close(t.Context()); e != nil {
		t.Fatal(e)
	}
	status, e = s.Status(t.Context())
	if e != nil || status.State != "closed" || status.LastSend.Outcome != "uncertain" {
		t.Fatal(status, e)
	}
	// A caller cannot mutate saved status evidence by changing a returned copy.
	status.LastSend.Outcome = "forged"
	next, _ := s.Status(t.Context())
	if next.LastSend.Outcome != "uncertain" {
		t.Fatal(next)
	}
	reopened := sessionOpen(t, cfg)
	rows, e := reopened.store.Next(t.Context(), 1)
	if e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
}
func TestCapacityStatusRefusesOverflowAndNoPayloadExposure(t *testing.T) {
	s := sessionOpen(t, sessionConfig(t))
	s.Retain(t.Context(), reading())
	if _, e := s.store.CapacityStatus(nil); !errors.Is(e, ErrInput) {
		t.Fatal(e)
	}
	if _, e := s.store.db.Exec(`INSERT INTO admission_inputs(event_id,observed_at,topic,payload,digest) SELECT 'other',observed_at,topic,payload,digest FROM admission_inputs`); e != nil {
		t.Fatal(e)
	}
	if got, e := s.store.CapacityStatus(t.Context()); !errors.Is(e, ErrEvidence) || got.Capacity != 0 {
		t.Fatal(got, e)
	}
	if got, e := s.Status(t.Context()); !errors.Is(e, ErrEvidence) || got.Capacity != nil {
		t.Fatal(got, e)
	}
	// Status schema deliberately carries only counts, lifecycle and last batch.
	b, _ := json.Marshal(SessionStatus{})
	for _, field := range []string{"payload", "endpoint", "private_key", "path"} {
		if strings.Contains(string(b), field) {
			t.Fatal("private field exposed", field)
		}
	}
}
