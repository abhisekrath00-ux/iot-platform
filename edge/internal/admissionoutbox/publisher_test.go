package admissionoutbox

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"

	"testing"
	"time"
)

type exchangeFunc func(context.Context, Item) ([]byte, error)

func (f exchangeFunc) AdmitOnce(c context.Context, i Item) ([]byte, error) { return f(c, i) }
func publisher(t *testing.T, s *Store, c ReceiptTrust, f exchangeFunc, n int) *Publisher {
	t.Helper()
	p, e := NewPublisher(PublisherConfig{Enabled: true, Store: s, Trust: c, Exchange: f, BatchLimit: n})
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestPublisherRefusesBeforeAnySend(t *testing.T) {
	s := openStore(t, config(t, 2))
	c, k := trust(t)
	calls := 0
	f := exchangeFunc(func(_ context.Context, it Item) ([]byte, error) { calls++; return token(t, receipt(it), c, k), nil })
	for _, mode := range []string{"disabled", "store", "exchange", "typednil", "limit", "trust", "key"} {
		bad := PublisherConfig{Enabled: true, Store: s, Trust: c, Exchange: f, BatchLimit: 2}
		switch mode {
		case "disabled":
			bad.Enabled = false
		case "store":
			bad.Store = nil
		case "exchange":
			bad.Exchange = nil
		case "typednil":
			var nilFunc exchangeFunc
			bad.Exchange = nilFunc
		case "limit":
			bad.BatchLimit = 257
		case "trust":
			bad.Trust.Enabled = false
		case "key":
			bad.Trust.PublicKey = nil
		}
		if p, e := NewPublisher(bad); p != nil || e == nil {
			t.Fatal(mode, p, e)
		}
	}
	p := publisher(t, s, c, f, 2)
	if r, e := p.RunOnce(nil); r.Attempted != 0 || e == nil {
		t.Fatal(r, e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if r, e := p.RunOnce(ctx); r.Attempted != 0 || !errors.Is(e, context.Canceled) {
		t.Fatal(r, e)
	}
	if r, e := p.RunOnce(t.Context()); e != nil || r != (BatchResult{}) {
		t.Fatal(r, e)
	}
	mustPut(t, s, reading())
	other := reading()
	other.EventID = "foreign"
	other.TenantID = "foreign"
	mustPut(t, s, other)
	if r, e := p.RunOnce(t.Context()); r.Attempted != 0 || r.Selected != 2 || !errors.Is(e, ErrSignature) {
		t.Fatal(r, e)
	}
	if calls != 0 {
		t.Fatal(calls)
	}
}
func TestPublisherLostAdmissionResponseExplicitSameInputReconciliation(t *testing.T) {
	s := openStore(t, config(t, 3))
	first := mustPut(t, s, reading())
	r := reading()
	r.EventID = "second"
	second := mustPut(t, s, r)
	r.EventID = "third"
	third := mustPut(t, s, r)
	c, k := trust(t)
	var attempts []Item
	lost := errors.New("synthetic commit succeeded but response lost")
	committed := map[string]bool{}
	p := publisher(t, s, c, func(_ context.Context, it Item) ([]byte, error) {
		attempts = append(attempts, it)
		prior := committed[it.Reading.EventID]
		committed[it.Reading.EventID] = true
		if len(attempts) == 1 {
			return nil, lost
		}
		r := receipt(it)
		r.New = !prior
		return token(t, r, c, k), nil
	}, 2)
	got, e := p.RunOnce(t.Context())
	if !errors.Is(e, lost) || got.Selected != 2 || got.Attempted != 1 || got.Reconciled != 0 || got.StoppedSequence != first.Sequence {
		t.Fatal(got, e)
	}
	rows, e := s.Next(t.Context(), 3)
	if e != nil || len(rows) != 3 {
		t.Fatal(rows, e)
	}
	// Only this explicit second call reconciles the same identity. No sleep, worker
	// or internal retry occurred after the lost response. Snapshot stays bounded.
	got, e = p.RunOnce(t.Context())
	if e != nil || got.Selected != 2 || got.Attempted != 2 || got.Reconciled != 2 || got.StoppedSequence != 0 {
		t.Fatal(got, e)
	}
	if len(attempts) != 3 || attempts[0].Sequence != first.Sequence || attempts[1].Sequence != first.Sequence || !bytes.Equal(attempts[0].Payload, attempts[1].Payload) || attempts[2].Sequence != second.Sequence {
		t.Fatal(attempts)
	}
	rows, e = s.Next(t.Context(), 3)
	if e != nil || len(rows) != 1 || rows[0].Sequence != third.Sequence {
		t.Fatal(rows, e)
	}
}
func TestPublisherStopsOnUntrustedBrokerACKFailureAndCancellation(t *testing.T) {
	for _, mode := range []string{"broker-only", "unsigned", "bad-signature", "wrong-route", "send-error", "cancel", "corrupt-retained", "panic"} {
		t.Run(mode, func(t *testing.T) {
			s := openStore(t, config(t, 3))
			first := mustPut(t, s, reading())
			r := reading()
			r.EventID = "second"
			mustPut(t, s, r)
			c, k := trust(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			p := publisher(t, s, c, func(_ context.Context, it Item) ([]byte, error) {
				calls++
				switch mode {
				case "broker-only":
					return nil, nil
				case "unsigned":
					return it.Payload, nil
				case "bad-signature":
					b := token(t, receipt(it), c, k)
					b[len(b)-5] ^= 1
					return b, nil
				case "wrong-route":
					r := receipt(it)
					r.GatewayID = "other"
					return token(t, r, c, k), nil
				case "send-error":
					return nil, errors.New("send failed after possible commit")
				case "cancel":
					cancel()
				case "corrupt-retained":
					if _, e := s.db.Exec(`UPDATE admission_inputs SET digest='corrupt' WHERE sequence=?`, it.Sequence); e != nil {
						t.Fatal(e)
					}
				case "panic":
					panic("synthetic transport panic")
				}
				return token(t, receipt(it), c, k), nil
			}, 3)
			if mode == "panic" {
				func() {
					defer func() {
						if recover() == nil {
							t.Fatal("panic lost")
						}
					}()
					p.RunOnce(ctx)
				}()
				p.exchange = exchangeFunc(func(context.Context, Item) ([]byte, error) { return nil, errors.New("stopped") })
				if _, e := p.RunOnce(ctx); errors.Is(e, ErrPublisherBusy) {
					t.Fatal("capacity leaked")
				}
			} else {
				got, e := p.RunOnce(ctx)
				if e == nil || got.Attempted != 1 || got.Reconciled != 0 || got.StoppedSequence != first.Sequence {
					t.Fatal(got, e)
				}
			}
			var n int
			if e := s.db.QueryRow(`SELECT count(*) FROM admission_inputs`).Scan(&n); e != nil || n != 2 {
				t.Fatal(n, e)
			}
			if mode != "panic" && calls != 1 {
				t.Fatal("automatic retry/fanout", calls)
			}
		})
	}
}
func TestPublisherConcurrentCloseDrainAndFrozenTrust(t *testing.T) {
	s := openStore(t, config(t, 2))
	mustPut(t, s, reading())
	c, k := trust(t)
	signingTrust := c
	signingTrust.PublicKey = append(ed25519.PublicKey(nil), c.PublicKey...)
	entered := make(chan struct{})
	release := make(chan struct{})
	p := publisher(t, s, c, func(_ context.Context, it Item) ([]byte, error) {
		close(entered)
		<-release
		return token(t, receipt(it), signingTrust, k), nil
	}, 1)
	// Mutating caller pin after construction must not retarget the trusted key.
	for i := range c.PublicKey {
		c.PublicKey[i] ^= 1
	}
	done := make(chan error, 1)
	go func() { _, e := p.RunOnce(t.Context()); done <- e }()
	<-entered
	if got, e := p.RunOnce(t.Context()); !errors.Is(e, ErrPublisherBusy) || got.Attempted != 0 {
		t.Fatal(got, e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if e := p.Close(ctx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	if got, e := p.RunOnce(t.Context()); !errors.Is(e, ErrPublisherClosed) || got.Attempted != 0 {
		t.Fatal(got, e)
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if e := p.Close(t.Context()); e != nil {
		t.Fatal(e)
	}
}
func TestPublisherPartialProgressStopsBeforeThirdRow(t *testing.T) {
	s := openStore(t, config(t, 3))
	mustPut(t, s, reading())
	r := reading()
	r.EventID = "second"
	second := mustPut(t, s, r)
	r.EventID = "third"
	mustPut(t, s, r)
	c, k := trust(t)
	calls := 0
	p := publisher(t, s, c, func(_ context.Context, it Item) ([]byte, error) {
		calls++
		if calls == 2 {
			return nil, errors.New("synthetic second failure")
		}
		return token(t, receipt(it), c, k), nil
	}, 3)
	got, e := p.RunOnce(t.Context())
	if e == nil || got.Selected != 3 || got.Attempted != 2 || got.Reconciled != 1 || got.StoppedSequence != second.Sequence || calls != 2 {
		t.Fatal(got, e, calls)
	}
	rows, e := s.Next(t.Context(), 3)
	if e != nil || len(rows) != 2 || rows[0].Sequence != second.Sequence {
		t.Fatal(rows, e)
	}
}
func TestPublisherEmptyNextInvocationDoesNotResend(t *testing.T) {
	// This explicitly documents the bounded in-process gate: it is not a lease
	// across instances/processes. Do not claim global exactly-once publishing.
	s := openStore(t, config(t, 1))
	mustPut(t, s, reading())
	c, k := trust(t)
	calls := 0
	p := publisher(t, s, c, func(_ context.Context, it Item) ([]byte, error) {
		calls++
		return token(t, receipt(it), c, k), nil
	}, 1)
	if r, e := p.RunOnce(t.Context()); e != nil || r.Reconciled != 1 {
		t.Fatal(r, e)
	}
	if r, e := p.RunOnce(t.Context()); e != nil || r.Attempted != 0 {
		t.Fatal(r, e)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}

func TestPublisherCorruptBatchRefusesBeforeFirstSend(t *testing.T) {
	s := openStore(t, config(t, 2))
	mustPut(t, s, reading())
	r := reading()
	r.EventID = "second"
	second := mustPut(t, s, r)
	if _, e := s.db.Exec(`UPDATE admission_inputs SET digest='bad' WHERE sequence=?`, second.Sequence); e != nil {
		t.Fatal(e)
	}
	c, _ := trust(t)
	calls := 0
	p := publisher(t, s, c, func(context.Context, Item) ([]byte, error) { calls++; return nil, nil }, 2)
	if r, e := p.RunOnce(t.Context()); !errors.Is(e, ErrEvidence) || r.Attempted != 0 || calls != 0 {
		t.Fatal(r, e, calls)
	}
}
func TestPublisherFailureReopensSameInputAndCopiedTransportPayload(t *testing.T) {
	cfg := config(t, 1)
	s := openStore(t, cfg)
	it := mustPut(t, s, reading())
	c, k := trust(t)
	p := publisher(t, s, c, func(_ context.Context, sent Item) ([]byte, error) {
		sent.Payload[0] = '['
		return nil, errors.New("synthetic uncertain send")
	}, 1)
	if r, e := p.RunOnce(t.Context()); e == nil || r.Reconciled != 0 {
		t.Fatal(r, e)
	}
	if e := p.Close(t.Context()); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s = openStore(t, cfg)
	rows, e := s.Next(t.Context(), 1)
	if e != nil || len(rows) != 1 || !bytes.Equal(rows[0].Payload, it.Payload) {
		t.Fatal(rows, e)
	}
	next := publisher(t, s, c, func(_ context.Context, sent Item) ([]byte, error) {
		if sent.Sequence != it.Sequence || !bytes.Equal(sent.Payload, it.Payload) {
			t.Fatal("reopen changed identity")
		}
		r := receipt(sent)
		r.New = false
		return token(t, r, c, k), nil
	}, 1)
	if r, e := next.RunOnce(t.Context()); e != nil || r.Reconciled != 1 {
		t.Fatal(r, e)
	}
}
