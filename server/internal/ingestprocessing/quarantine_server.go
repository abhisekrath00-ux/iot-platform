package ingestprocessing

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/authorityschema"
)

var ErrQuarantineServer = errors.New("staged quarantine server requires explicit exact95 and bounded direct TLS configuration")
var ErrQuarantineDrain = errors.New("staged quarantine handler drain unconfirmed; keep database and signing authority alive until WaitDrained confirms")

type GatewayQuarantineServerConfig struct {
	Enabled       bool
	Engine        Engine
	Certificate   tls.Certificate
	SessionSecret []byte
	// ReadOnlyInspection adds exact-input redacted admin inspection. False keeps
	// the original deny-only surface. It never adds execution or recovery.
	ReadOnlyInspection bool
}

// GatewayQuarantineServer is a separate default-off, direct HTTPS transport for
// the deny-only session controller, with separately opted-in redacted read-only
// inspection. No startup/router creates it. It is NOT the
// telemetry mTLS endpoint and trusts NO client certificate as a session. Tracked
// JWT verification and current database authorization remain inside each decision.
// New opens no listener, performs no migration and changes no authority. Exact95
// preflight is point-in-time, NOT backup/restore continuity or ongoing DDL safety.
// Certificate metadata and the session secret are detached; the caller must keep
// its crypto.Signer immutable/alive and the DB alive until WaitDrained confirms.
// Reconfiguration requires a NEW server, never automatic reopen or retry.
type GatewayQuarantineServer struct{ transport *AdmissionServer }

func NewGatewayQuarantineServer(ctx context.Context, c GatewayQuarantineServerConfig, dir string) (*GatewayQuarantineServer, error) {
	if !c.Enabled {
		return nil, ErrDisabled
	}
	if ctx == nil {
		return nil, ErrQuarantineServer
	}
	cfg, err := validatedServerTLS(AdmissionServerConfig{Certificate: c.Certificate}, false)
	if err != nil {
		return nil, ErrQuarantineServer
	}
	handler, err := NewGatewayQuarantineHTTP(c.Enabled, c.Engine, c.SessionSecret)
	if err != nil {
		return nil, err
	}
	if c.ReadOnlyInspection {
		inspection, err := NewGatewayInspectionHTTP(true, c.Engine, c.SessionSecret)
		if err != nil {
			return nil, err
		}
		denyOnly := handler
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == inspectionPath {
				inspection.ServeHTTP(w, r)
				return
			}
			denyOnly.ServeHTTP(w, r)
		})
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := authorityschema.Inspect(ctx, c.Engine.Pool, dir); err != nil {
		return nil, ErrQuarantineServer
	}
	// Share only the already-tested bounded listener and actual-handler lifetime
	// machinery, NOT the admission handler, routes, client CA or enrollment policy.
	// 64 accepted sockets include pre-TLS peers; excess stay in the OS backlog.
	// At most 16 handler calls. Each controller has four separate slots, so opted-in
	// inspection can coexist with at most four decisions and four inspection calls.
	transport := &AdmissionServer{handler: handler, drained: make(chan struct{})}
	transport.server = &http.Server{Handler: http.HandlerFunc(transport.handle), TLSConfig: cfg, TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 8192}
	return &GatewayQuarantineServer{transport: transport}, nil
}

// Serve takes the caller's explicitly opened listener ONCE. Cancellation closes
// entry, waits up to two seconds then force-closes connections. Connection close
// is NOT proof of handler/DB completion. A committed decision may lose its reply:
// inspect current state, never automatically retry or undo. No proxy/plaintext.
func (s *GatewayQuarantineServer) Serve(ctx context.Context, ln net.Listener) error {
	if s == nil || s.transport == nil {
		return ErrQuarantineServer
	}
	err := s.transport.Serve(ctx, ln)
	if errors.Is(err, ErrAdmissionDrain) {
		return ErrQuarantineDrain
	}
	if errors.Is(err, ErrAdmissionServer) {
		return ErrQuarantineServer
	}
	return err
}

// WaitDrained confirms permanently closed entry and all ACTUAL handler returns,
// not client receipt or rollback of a decision. It refuses before Serve starts.
func (s *GatewayQuarantineServer) WaitDrained(ctx context.Context) error {
	if s == nil || s.transport == nil {
		return ErrQuarantineServer
	}
	err := s.transport.WaitDrained(ctx)
	if errors.Is(err, ErrAdmissionServer) {
		return ErrQuarantineServer
	}
	return err
}
