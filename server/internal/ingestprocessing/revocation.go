package ingestprocessing

import (
	"context"
	"errors"
)

var ErrAdmissionRoute = errors.New("staged admission route absent or revoked")

// routeFence is deny-only state for one constructor-bound leaf, never a mutable
// enrollment or route replacement registry. Protected by AdmissionHTTP.routeMu.
type routeFence struct {
	revoked bool
	active  int
	drained chan struct{}
}

// RouteRevocation is an observation/wait handle, not a grant or receipt of
// rollback. Earlier admitted route handlers may still commit or have lost their
// responses. Wait confirms only their return, not provider delivery or rollback.
// No JSON, public endpoint, persistent revocation file or activation uses it.
type RouteRevocation struct{ done <-chan struct{} }

func (r *RouteRevocation) Wait(ctx context.Context) error {
	if r == nil || r.done == nil || ctx == nil {
		return ErrAdmissionRoute
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// RevokeRoute permanently denies NEW admission on this process-local handler for
// an EXISTING exact leaf binding. There is no undo/add/replace API. It performs no
// DB/socket/file/key operation, source read or send. In-flight calls that crossed
// beginRoute before this fence may still commit. Their returned wait handle can
// confirm they finished; cancellation/timeout is NOT confirmed completion.
// IMPORTANT: this denial is NOT durable across a newly constructed server. Its
// authority source must exclude revoked bindings on every future construction.
// No runtime caller or management endpoint invokes this method.
func (h *AdmissionHTTP) RevokeRoute(leaf [32]byte) (*RouteRevocation, error) {
	if h == nil {
		return nil, ErrAdmissionRoute
	}
	h.routeMu.Lock()
	defer h.routeMu.Unlock()
	state, ok := h.fences[leaf]
	if !ok {
		return nil, ErrAdmissionRoute
	}
	if !state.revoked {
		state.revoked = true
		if state.active == 0 {
			close(state.drained)
		}
	}
	return &RouteRevocation{done: state.drained}, nil
}
func (h *AdmissionHTTP) beginRoute(leaf [32]byte) error {
	h.routeMu.Lock()
	defer h.routeMu.Unlock()
	state, ok := h.fences[leaf]
	if !ok || state.revoked {
		return ErrAdmissionRoute
	}
	state.active++
	return nil
}
func (h *AdmissionHTTP) endRoute(leaf [32]byte) {
	h.routeMu.Lock()
	defer h.routeMu.Unlock()
	state := h.fences[leaf]
	state.active--
	if state.revoked && state.active == 0 {
		close(state.drained)
	}
}

// RevokeRoute exposes only the existing handler's deny-only fence. It does not
// authenticate an operator; an eventual owner/controller must do so separately.
func (s *AdmissionServer) RevokeRoute(leaf [32]byte) (*RouteRevocation, error) {
	if s == nil || s.admission == nil {
		return nil, ErrAdmissionRoute
	}
	return s.admission.RevokeRoute(leaf)
}
