package admissionoutbox

import (
	"context"
	"crypto/ed25519"
	"errors"
	"sync"
)

var ErrSessionClosed = errors.New("staged admission session closing or closed; no new operation started")

// CapacityStatus is an instantaneous retained-row count, not a health check,
// backlog delivery estimate or evidence of input/receipt validity. Other local
// collectors can change it immediately. Full means a NEW input cannot be saved;
// exact retained duplicates can still succeed and nothing is evicted.
type CapacityStatus struct {
	Retained  int  `json:"retained"`
	Capacity  int  `json:"capacity"`
	Available int  `json:"available"`
	Full      bool `json:"full"`
}

func (s *Store) CapacityStatus(ctx context.Context) (CapacityStatus, error) {
	if s == nil || s.db == nil {
		return CapacityStatus{}, ErrDisabled
	}
	if ctx == nil {
		return CapacityStatus{}, ErrInput
	}
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM admission_inputs").Scan(&n); err != nil {
		return CapacityStatus{}, err
	}
	if n < 0 || n > s.capacity {
		return CapacityStatus{}, ErrEvidence
	}
	return CapacityStatus{Retained: n, Capacity: s.capacity, Available: s.capacity - n, Full: n == s.capacity}, nil
}

type SessionConfig struct {
	Enabled    bool
	Store      Config
	HTTPS      HTTPSConfig
	Trust      ReceiptTrust
	BatchLimit int
}

// SendStatus records only this process session's LAST explicit call. It is not
// durable history or a queue-depth/effect-delivery receipt. An uncertain attempt
// may have committed remotely; only SAME retained input may be reconciled later.
type SendStatus struct {
	Batch   BatchResult `json:"batch"`
	Outcome string      `json:"outcome"` // admission-confirmed, idle, refused, uncertain
}
type SessionStatus struct {
	Sending  bool            `json:"sending"`             // current session only, not another process
	State    string          `json:"state"`               // open, closing, closed
	Capacity *CapacityStatus `json:"queue,omitempty"`     // absent after closing starts
	LastSend *SendStatus     `json:"last_send,omitempty"` // process-local only
}

// Session composes one scoped retained Store, explicit HTTPSExchange and pinned
// Publisher. No startup, route or collector opens it. Pairing/key authority must
// be supplied independently; constructing it is not enrollment. No daemon,
// retry, automatic collection, event/time generation or effect execution exists.
// All inputs must already carry a stable canonical identity and time.
type Session struct {
	mu              sync.Mutex
	closing, closed bool
	sending         bool
	active          int
	drained         chan struct{}
	closeMu         sync.Mutex
	store           *Store
	publisher       *Publisher
	tenant, gateway string
	last            *SendStatus
}

func OpenSession(ctx context.Context, c SessionConfig) (*Session, error) {
	if !c.Enabled {
		return nil, ErrDisabled
	}
	// Validate configuration before a path is opened. Each component's opt-in
	// remains explicit, never silently upgraded by the outer session flag.
	if ctx == nil || !c.Store.Enabled || !c.Trust.Enabled || !validAudience(c.Trust.Audience) || !validID(c.Trust.TenantID) || !validID(c.Trust.GatewayID) || len(c.Trust.PublicKey) != ed25519.PublicKeySize || c.BatchLimit < 1 || c.BatchLimit > 256 {
		return nil, ErrInput
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	exchange, err := NewHTTPSExchange(c.HTTPS)
	if err != nil {
		return nil, err
	}
	store, err := Open(ctx, c.Store)
	if err != nil {
		return nil, err
	}
	publisher, err := NewPublisher(PublisherConfig{Enabled: true, Store: store, Trust: c.Trust, Exchange: exchange, BatchLimit: c.BatchLimit})
	if err != nil {
		store.Close()
		return nil, err
	}
	return &Session{store: store, publisher: publisher, tenant: c.Trust.TenantID, gateway: c.Trust.GatewayID, drained: make(chan struct{})}, nil
}
func (s *Session) begin() error {
	if s == nil {
		return ErrSessionClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return ErrSessionClosed
	}
	s.active++
	return nil
}
func (s *Session) end() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active--
	if s.closing && s.active == 0 {
		close(s.drained)
	}
}

// Retain saves before sending; it NEVER invokes SendOnce. Full/conflict/error
// returns zero saved evidence. Foreign route refuses before touching the store.
func (s *Session) Retain(ctx context.Context, r Reading) (Item, error) {
	if err := s.begin(); err != nil {
		return Item{}, err
	}
	defer s.end()
	if r.TenantID != s.tenant || r.GatewayID != s.gateway {
		return Item{}, ErrSignature
	}
	return s.store.Put(ctx, r)
}
func (s *Session) SendOnce(ctx context.Context) (BatchResult, error) {
	if err := s.begin(); err != nil {
		return BatchResult{}, err
	}
	defer s.end()
	s.mu.Lock()
	if s.sending {
		s.mu.Unlock()
		return BatchResult{}, ErrPublisherBusy
	}
	s.sending = true
	s.mu.Unlock()
	result, err := s.publisher.RunOnce(ctx)
	outcome := "admission-confirmed"
	if err != nil {
		outcome = "refused"
		if result.Attempted > 0 {
			outcome = "uncertain"
		}
	} else if result.Attempted == 0 {
		outcome = "idle"
	}
	s.mu.Lock()
	s.last = &SendStatus{Batch: result, Outcome: outcome}
	s.sending = false
	s.mu.Unlock()
	return result, err
}

// Status remains available during/after Close, but no stale queue count is
// presented as current. No payload, key, endpoint or private filesystem path is
// exposed. Queue counts do not imply valid receipts or provider delivery.
func (s *Session) Status(ctx context.Context) (SessionStatus, error) {
	if s == nil {
		return SessionStatus{State: "closed"}, nil
	}
	s.mu.Lock()
	status := SessionStatus{State: "open", Sending: s.sending}
	if s.last != nil {
		copy := *s.last
		status.LastSend = &copy
	}
	if s.closing {
		status.State = "closing"
		if s.closed {
			status.State = "closed"
		}
		s.mu.Unlock()
		return status, nil
	}
	s.active++
	s.mu.Unlock()
	defer s.end()
	capacity, err := s.store.CapacityStatus(ctx)
	if err != nil {
		return status, err
	}
	status.Capacity = &capacity
	return status, nil
}

// Close permanently prevents new operations, then drains existing collection,
// status and SendOnce before closing the database. A timeout leaves it closing,
// retains the in-flight publisher ownership and does NOT cancel/rollback/resend.
// Call Close again to finish draining. The caller's send context bounds network
// work. No file/key/config is deleted, renamed or rewritten by Close.
func (s *Session) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		return ErrInput
	}
	s.mu.Lock()
	if !s.closing {
		s.closing = true
		if s.active == 0 {
			close(s.drained)
		}
	}
	done := s.drained
	s.mu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil
	}
	if err := s.publisher.Close(ctx); err != nil {
		return err
	}
	if err := s.store.Close(); err != nil {
		return err
	}
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}
