package ingestprocessing

import (
	"context"
	"errors"
	"net"
	"reflect"
	"sync"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/authorityschema"
)

var ErrStagedGatewayRuntime = errors.New("staged gateway runtime requires explicit matching guarded transports and independent selected authority evidence")
var ErrStagedGatewayDrain = errors.New("staged gateway runtime handler drain unconfirmed; keep database and signing keys alive until WaitDrained confirms")

type StagedGatewayRuntimeConfig struct {
	Enabled            bool
	Admission          AdmissionServerConfig
	Management         GatewayQuarantineServerConfig
	MigrationDirectory string
	// Preserve trusted ORIGINAL pre-backup provenance independently. The caller
	// supplies this value; the constructor cannot authenticate its origin/freshness.
	// A matching baseline read from the same DB/backup does NOT establish continuity.
	SelectedAuthority authorityschema.IdentityEvidence
}

// StagedGatewayRuntime composes ONLY guarded admission and deny-only management.
// It is default-off, absent from startup/routers/normal Update and opens no sockets
// at construction. Both transports MUST share one explicit enabled Engine pool.
// No migration, enrollment, key generation, restore, worker, effect execution or
// restart exists here. Supplying a witness/flags is NOT operator/owner permission.
// Preflight is point-in-time selected-state equality, NOT full security/restore
// acceptance. Later admin/admission requests retain their own transaction guards.
// Caller must keep DB and immutable signing keys alive until WaitDrained confirms.
type StagedGatewayRuntime struct {
	admission  *AdmissionServer
	management *GatewayQuarantineServer
	mu         sync.Mutex
	started    bool
	finished   chan struct{}
}

func NewStagedGatewayRuntime(ctx context.Context, c StagedGatewayRuntimeConfig) (*StagedGatewayRuntime, error) {
	if !c.Enabled {
		return nil, ErrDisabled
	}
	if ctx == nil || !c.Admission.Enabled || !c.Management.Enabled || !c.Admission.Admission.Engine.Enabled || !c.Management.Engine.Enabled || c.Admission.Admission.Engine.Pool == nil || c.Admission.Admission.Engine.Pool != c.Management.Engine.Pool || !c.Admission.Admission.DurableDenials || !c.Admission.Admission.GatewayAuthority {
		return nil, ErrStagedGatewayRuntime
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// The maintenance witness check briefly fences ALL selected-table writers.
	// This additional cross-tenant operational cost is NOT a runtime health probe.
	if err := authorityschema.VerifyIdentityEvidence(ctx, c.Management.Engine.Pool, c.MigrationDirectory, c.SelectedAuthority); err != nil {
		return nil, ErrStagedGatewayRuntime
	}
	admission, err := NewGuardedAdmissionServer(ctx, c.Admission, c.MigrationDirectory)
	if err != nil {
		return nil, ErrStagedGatewayRuntime
	}
	management, err := NewGatewayQuarantineServer(ctx, c.Management, c.MigrationDirectory)
	if err != nil {
		return nil, ErrStagedGatewayRuntime
	}
	if ctx.Err() != nil {
		return nil, ErrStagedGatewayRuntime
	}
	return &StagedGatewayRuntime{admission: admission, management: management, finished: make(chan struct{})}, nil
}

func validRuntimeListener(l net.Listener) bool {
	if l == nil {
		return false
	}
	v := reflect.ValueOf(l)
	if (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) && v.IsNil() {
		return false
	}
	a := l.Addr()
	return a != nil && a.Network() != "" && a.String() != ""
}

// Serve takes ownership of BOTH explicit distinct listeners only after validation
// and the single-use check. No listener is opened here. Once ownership transfers,
// both are closed on ANY exit, including a cancellation before child Serve begins.
// The first child exit permanently cancels its sibling; there is no retry/reopen.
// A lost response may follow a real commit. Inspect, never blindly retry effects.
// Listener/transport failures are retained as a generic error without DB/key data.
// Child connection-close is NOT actual handler drain. ErrStagedGatewayDrain means
// shared DB/keys must remain alive until a separate WaitDrained confirms completion.
func (s *StagedGatewayRuntime) Serve(ctx context.Context, admission, management net.Listener) error {
	if s == nil || s.admission == nil || s.management == nil || s.management.transport == nil || s.finished == nil || ctx == nil || !validRuntimeListener(admission) || !validRuntimeListener(management) {
		return ErrStagedGatewayRuntime
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if admission.Addr().Network() == management.Addr().Network() && admission.Addr().String() == management.Addr().String() {
		return ErrStagedGatewayRuntime
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return ErrStagedGatewayRuntime
	}
	s.started = true
	s.mu.Unlock()
	defer close(s.finished)
	defer admission.Close()
	defer management.Close()
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, 2)
	go func() { results <- s.admission.Serve(child, admission) }()
	go func() { results <- s.management.Serve(child, management) }()
	first := <-results
	cancel()
	second := <-results
	if s.hasActiveHandlers() {
		return ErrStagedGatewayDrain
	}
	for _, err := range []error{first, second} {
		if err != nil && !errors.Is(err, context.Canceled) {
			return ErrStagedGatewayRuntime
		}
	}
	return nil
}
func (s *StagedGatewayRuntime) hasActiveHandlers() bool {
	for _, transport := range []*AdmissionServer{s.admission, s.management.transport} {
		transport.mu.Lock()
		active := transport.active
		transport.mu.Unlock()
		if active != 0 {
			return true
		}
	}
	return false
}

// WaitDrained refuses before Serve starts. It waits until BOTH child Serve calls
// exited and every actually-entered handler returned. A child that never started
// owns no handlers. It does not prove response receipt, rollback or backup safety.
func (s *StagedGatewayRuntime) WaitDrained(ctx context.Context) error {
	if s == nil || s.admission == nil || s.management == nil || s.management.transport == nil || s.finished == nil || ctx == nil {
		return ErrStagedGatewayRuntime
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	started := s.started
	s.mu.Unlock()
	if !started {
		return ErrStagedGatewayRuntime
	}
	select {
	case <-s.finished:
	case <-ctx.Done():
		return ctx.Err()
	}
	for _, transport := range []*AdmissionServer{s.admission, s.management.transport} {
		transport.mu.Lock()
		childStarted := transport.started
		transport.mu.Unlock()
		if !childStarted {
			continue
		}
		if err := transport.WaitDrained(ctx); err != nil {
			return err
		}
	}
	return nil
}
