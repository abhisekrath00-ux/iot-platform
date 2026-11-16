package admissionoutbox

import (
	"context"
	"crypto/ed25519"
	"errors"
	"reflect"
	"sync"
)

var ErrPublisherBusy = errors.New("staged publisher already running; no second send started")
var ErrPublisherClosed = errors.New("staged publisher closed; no new send started")

// Exchange sends ONE retained input and returns signed SERVER ADMISSION bytes,
// not broker PUBACK. The implementation MUST honor context, perform no internal
// retry/reconnect/redirect/fanout, and never substitute event/time or mutate the
// item concurrently. An error/empty response can follow a real server commit.
// It must not report provider/effect completion. No production implementation
// exists. In particular mqttc.Publish alone cannot satisfy this contract.
type Exchange interface {
	AdmitOnce(context.Context, Item) ([]byte, error)
}

type PublisherConfig struct {
	Enabled    bool
	Store      *Store
	Trust      ReceiptTrust
	Exchange   Exchange
	BatchLimit int
}

// Publisher is an explicit one-attempt batch adapter, not a daemon, retry queue
// worker or runtime enable switch. A constructed Publisher must not be copied.
// The caller owns explicit later SAME-input reconciliation and visible status.
// No startup caller constructs it and the existing production queue is unchanged.
type Publisher struct {
	mu             sync.Mutex
	active, closed bool
	done           chan struct{}
	store          *Store
	trust          ReceiptTrust
	exchange       Exchange
	limit          int
}

// BatchResult distinguishes snapshot/attempted/positively reconciled counts.
// Attempted includes the last invocation even when it failed or was uncertain.
// Selected is a bounded snapshot, NOT a live queue depth. On failure stop before the next row.
// Reconciled means signed server admission only, never effects/provider delivery.
type BatchResult struct {
	Selected        int
	Attempted       int
	Reconciled      int
	StoppedSequence int64
}

func NewPublisher(c PublisherConfig) (*Publisher, error) {
	if !c.Enabled {
		return nil, ErrDisabled
	}
	if c.Store == nil || c.Store.db == nil || c.Exchange == nil || c.BatchLimit < 1 || c.BatchLimit > 256 {
		return nil, ErrInput
	}
	if !c.Trust.Enabled || !validAudience(c.Trust.Audience) || !validID(c.Trust.TenantID) || !validID(c.Trust.GatewayID) || len(c.Trust.PublicKey) != ed25519.PublicKeySize {
		return nil, ErrSignature
	}
	// Freeze pin scope rather than consulting caller-mutable config mid-batch.
	v := reflect.ValueOf(c.Exchange)
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface:
		if v.IsNil() {
			return nil, ErrInput
		}
	}
	trust := c.Trust
	trust.PublicKey = append(ed25519.PublicKey(nil), trust.PublicKey...)
	return &Publisher{store: c.Store, trust: trust, exchange: c.Exchange, limit: c.BatchLimit}, nil
}
func (p *Publisher) enter() error {
	if p == nil {
		return ErrPublisherClosed
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrPublisherClosed
	}
	if p.active {
		return ErrPublisherBusy
	}
	p.active = true
	p.done = make(chan struct{})
	return nil
}
func (p *Publisher) leave() { p.mu.Lock(); p.active = false; close(p.done); p.mu.Unlock() }

// RunOnce snapshots at most the configured bound and preflights the WHOLE batch
// before any send. It sends sequentially, at most once per selected row. Errors,
// empty/bad receipts, uncertain commit or cancellation stop the batch. A later
// explicit invocation may reconcile the SAME retained input; no automatic retry
// or new identity is created. Store and Exchange must be dedicated to this
// Publisher while running; other processes/instances are NOT locked out by this
// in-process gate. Runtime cross-process ownership remains an acceptance gate.
func (p *Publisher) RunOnce(ctx context.Context) (result BatchResult, err error) {
	if err = p.enter(); err != nil {
		return result, err
	}
	defer p.leave()
	if ctx == nil {
		return result, ErrInput
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	items, err := p.store.Next(ctx, p.limit)
	if err != nil {
		return result, err
	}
	result.Selected = len(items)
	for _, it := range items {
		if it.Reading.TenantID != p.trust.TenantID || it.Reading.GatewayID != p.trust.GatewayID {
			return result, ErrSignature
		}
	}
	for _, it := range items {
		result.StoppedSequence = it.Sequence
		if err = ctx.Err(); err != nil {
			return result, err
		}
		// Next already returned owned byte slices. Give transport its own copy so it
		// cannot rewrite the retained snapshot used for local matching.
		send := it
		send.Payload = append([]byte(nil), it.Payload...)
		result.Attempted++
		wire, e := p.exchange.AdmitOnce(ctx, send)
		if e != nil {
			return result, e
		}
		if err = ctx.Err(); err != nil {
			return result, err
		}
		// Includes signature/scope/full identity and atomic retained-row reread.
		ok, e := p.store.ReconcileSigned(ctx, it.Sequence, wire, p.trust)
		if e != nil {
			return result, e
		}
		if !ok {
			return result, ErrEvidence
		}
		result.Reconciled++
	}
	result.StoppedSequence = 0
	return result, nil
}

// Close permanently refuses new batches and waits for the current one, including
// its signed reconciliation. Timeout stays closed; it does not cancel transport,
// claim rollback or trigger another send. The caller's run context bounds I/O.
// A later Close may wait again. A misbehaving Exchange can still fail to drain.
func (p *Publisher) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		return ErrInput
	}
	p.mu.Lock()
	p.closed = true
	active, done := p.active, p.done
	p.mu.Unlock()
	if !active {
		return nil
	}
	select {
	case <-done:
		return nil
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
