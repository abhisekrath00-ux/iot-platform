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
var ErrStagedGatewayDrain = errors.New("staged gateway runtime handler or execution drain unconfirmed; keep database, signing keys and sender alive until WaitDrained confirms")

type StagedGatewayRuntimeConfig struct {
	Enabled            bool
	Admission          AdmissionServerConfig
	Management         GatewayQuarantineServerConfig
	MigrationDirectory string
	// Preserve trusted ORIGINAL pre-backup provenance independently. The caller
	// supplies this value; the constructor cannot authenticate its origin/freshness.
	// A matching baseline read from the same DB/backup does NOT establish continuity.
	SelectedAuthority authorityschema.IdentityEvidence
	// Execution is separately explicit. Its pool AND exact leaf/topic set must
	// match Admission; nil preserves admission/management-only behavior.
	Execution *GuardedExecutionConfig
}

// StagedGatewayRuntime composes guarded admission and deny-only management,
// with separately opted-in explicit batch execution. It never selects work.
// It is default-off, absent from startup/routers/normal Update and opens no sockets
// at construction. Both transports MUST share one explicit enabled Engine pool.
// No migration, enrollment, key generation, restore, worker, automatic effect or
// restart exists here. Supplying a witness/flags is NOT operator/owner permission.
// Preflight is point-in-time selected-state equality, NOT full security/restore
// acceptance. Later admin/admission requests retain their own transaction guards.
// Caller must keep DB and immutable signing keys alive until WaitDrained confirms.
type StagedGatewayRuntime struct {
	admission        *AdmissionServer
	management       *GatewayQuarantineServer
	mu               sync.Mutex
	started          bool
	finished         chan struct{}
	executor         *GuardedExecutor
	executionContext context.Context
	executionClosing bool
	executionActive  int
	executionDrained chan struct{}
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
	var executor *GuardedExecutor
	if c.Execution != nil {
		ec := *c.Execution
		if !ec.Enabled || ec.Engine.Pool != c.Management.Engine.Pool || ec.MigrationDirectory != c.MigrationDirectory || len(ec.Routes) != len(admission.admission.routes) {
			return nil, ErrStagedGatewayRuntime
		}
		for _, route := range ec.Routes {
			if topic, ok := admission.admission.routes[route.LeafSHA256]; !ok || topic != route.Topic {
				return nil, ErrStagedGatewayRuntime
			}
		}
		executor, err = NewGuardedExecutor(ctx, ec)
		if err != nil {
			return nil, ErrStagedGatewayRuntime
		}
	}
	return &StagedGatewayRuntime{admission: admission, management: management, finished: make(chan struct{}), executor: executor, executionDrained: make(chan struct{})}, nil
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
// shared DB/keys/Sender must remain alive until a separate WaitDrained confirms
// actual handlers AND separately opted-in explicit executions returned.
func (s *StagedGatewayRuntime) Serve(ctx context.Context, admission, management net.Listener) error {
	if s == nil || s.admission == nil || s.management == nil || s.management.transport == nil || s.finished == nil || s.executionDrained == nil || ctx == nil || !validRuntimeListener(admission) || !validRuntimeListener(management) {
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
	child, cancel := context.WithCancel(ctx)
	s.started = true
	s.executionContext = child
	s.mu.Unlock()
	defer close(s.finished)
	defer admission.Close()
	defer management.Close()
	defer cancel()
	results := make(chan error, 2)
	go func() { results <- s.admission.Serve(child, admission) }()
	go func() { results <- s.management.Serve(child, management) }()
	first := <-results
	s.closeExecution()
	cancel()
	second := <-results
	if stagedChildUndrained(first) || stagedChildUndrained(second) || s.hasActiveHandlers() || s.hasActiveExecution() {
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
// exited and every actually-entered handler and opted-in batch returned. A child that never started
// owns no handlers. It does not prove response receipt, rollback or backup safety.
func (s *StagedGatewayRuntime) WaitDrained(ctx context.Context) error {
	if s == nil || s.admission == nil || s.management == nil || s.management.transport == nil || s.finished == nil || s.executionDrained == nil || ctx == nil {
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
	select {
	case <-s.executionDrained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// closeExecution permanently seals entry. It does not imply an arbitrary Sender
// obeyed cancellation or rolled back an effect. Only actual return releases it.
func (s *StagedGatewayRuntime) closeExecution() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.executionClosing {
		s.executionClosing = true
		if s.executionActive == 0 {
			close(s.executionDrained)
		}
	}
}
func (s *StagedGatewayRuntime) hasActiveExecution() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.executionActive != 0
}

// ExecuteOnce selects nothing automatically. This separate opt-in in-process
// entry is NOT an authenticated admin endpoint or grant for sender/recipients.
// It admits only during the one live Serve lifetime. Parent/caller cancellation
// cancels its guarded batch, but DB/Sender must stay alive until actual return.
// A canceled caller still may own an uncertain committed claim/provider effect.
func (s *StagedGatewayRuntime) ExecuteOnce(ctx context.Context, tenant, event string, at time.Time, sender Sender) error {
	if s == nil || ctx == nil {
		return ErrStagedGatewayRuntime
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.executor == nil || !s.started || s.executionClosing || s.executionContext == nil || s.executionContext.Err() != nil {
		s.mu.Unlock()
		return ErrStagedGatewayRuntime
	}
	life := s.executionContext
	s.executionActive++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.executionActive--
		if s.executionClosing && s.executionActive == 0 {
			close(s.executionDrained)
		}
	}()
	batch, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(life, cancel)
	defer stop()
	if life.Err() != nil {
		cancel()
	}
	return s.executor.ExecuteOnce(batch, tenant, event, at, sender)
}

// Preserve a child's explicit uncertainty even if its last handler returns
// between the child Serve result and the aggregate active-count observation.
// The caller still uses WaitDrained for positive resource-lifetime evidence.
func stagedChildUndrained(err error) bool {
	return errors.Is(err, ErrAdmissionDrain) || errors.Is(err, ErrQuarantineDrain)
}
