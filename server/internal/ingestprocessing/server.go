package ingestprocessing

import (
	"bytes"
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

var ErrAdmissionDrain = errors.New("staged admission handler drain unconfirmed; keep database and signing authority alive until WaitDrained confirms")

var ErrAdmissionServer = errors.New("staged admission server requires explicit bounded mutual TLS configuration")

type AdmissionServerConfig struct {
	Enabled     bool
	Admission   AdmissionHTTPConfig
	Certificate tls.Certificate
	ClientCAs   *x509.CertPool
}

// AdmissionServer is a dedicated default-off transport, not a runtime endpoint
// or pairing/enrollment service. NewAdmissionServer opens no listener. Serve
// requires a caller-owned, explicitly opened listener in an authorized environment.
// No startup uses it. Frozen certificate routes must be replaced via a NEW server;
// it supplies no hot rotation, revocation list or owner activation. The caller
// must keep its crypto.Signer immutable/alive and close its DB after Serve drains.
// TLS termination proxies are unsupported. Plaintext is never served.
type AdmissionServer struct {
	server  *http.Server
	mu      sync.Mutex
	started bool
	closing bool
	active  int
	drained chan struct{}
	handler http.Handler
}

func NewAdmissionServer(c AdmissionServerConfig) (*AdmissionServer, error) {
	if !c.Enabled {
		return nil, ErrDisabled
	}
	if c.ClientCAs == nil || len(c.ClientCAs.Subjects()) == 0 || len(c.Certificate.Certificate) == 0 || c.Certificate.PrivateKey == nil {
		return nil, ErrAdmissionServer
	}
	leaf, err := x509.ParseCertificate(c.Certificate.Certificate[0])
	if err != nil {
		return nil, ErrAdmissionServer
	}
	signer, ok := c.Certificate.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, ErrAdmissionServer
	}
	a, err := x509.MarshalPKIXPublicKey(leaf.PublicKey)
	if err != nil {
		return nil, ErrAdmissionServer
	}
	b, err := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil || !bytes.Equal(a, b) {
		return nil, ErrAdmissionServer
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) || leaf.IsCA {
		return nil, ErrAdmissionServer
	}
	serverUsage := false
	for _, u := range leaf.ExtKeyUsage {
		if u == x509.ExtKeyUsageServerAuth {
			serverUsage = true
		}
	}
	if !serverUsage || len(leaf.DNSNames)+len(leaf.IPAddresses) == 0 {
		return nil, ErrAdmissionServer
	}
	handler, err := NewAdmissionHTTP(c.Admission)
	if err != nil {
		return nil, err
	}
	cert := c.Certificate
	cert.Certificate = make([][]byte, len(c.Certificate.Certificate))
	for i, der := range c.Certificate.Certificate {
		cert.Certificate[i] = append([]byte(nil), der...)
	}
	// Parse the detached bytes too: x509 leaf slices may alias their DER.
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, ErrAdmissionServer
	}
	cert.OCSPStaple = append([]byte(nil), c.Certificate.OCSPStaple...)
	cert.SignedCertificateTimestamps = make([][]byte, len(c.Certificate.SignedCertificateTimestamps))
	for i, b := range c.Certificate.SignedCertificateTimestamps {
		cert.SignedCertificateTimestamps[i] = append([]byte(nil), b...)
	}
	cert.SupportedSignatureAlgorithms = append([]tls.SignatureScheme(nil), c.Certificate.SupportedSignatureAlgorithms...)
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: c.ClientCAs.Clone(), NextProtos: []string{"http/1.1"}, SessionTicketsDisabled: true}
	s := &AdmissionServer{handler: handler, drained: make(chan struct{})}
	s.server = &http.Server{Handler: http.HandlerFunc(s.handle), TLSConfig: cfg, TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 8192}
	return s, nil
}

// Serve runs ONCE. Cancellation permanently closes admission, drains active
// requests for up to two seconds, then closes remaining connections. A lost
// response or timeout can follow a committed transaction: retain SAME input for
// explicit reconciliation. No automatic re-admission/effect retry is performed.
// Forced close with a still-active handler returns ErrAdmissionDrain, NOT nil.
// Keep shared DB/signer alive until WaitDrained subsequently confirms completion.
// Listener ownership transfers only once serving starts, including on errors.
// Cancelled/nil/reused calls refuse before taking ownership of a new listener.
func (s *AdmissionServer) Serve(ctx context.Context, ln net.Listener) error {
	if s == nil || s.server == nil || s.drained == nil || ctx == nil || ln == nil {
		return ErrAdmissionServer
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return ErrAdmissionServer
	}
	s.started = true
	s.mu.Unlock()
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			s.closeAdmission()
			drain, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := s.server.Shutdown(drain); err != nil {
				_ = s.server.Close()
			}
		case <-done:
			s.closeAdmission()
			_ = s.server.Close()
		}
	}()
	err := s.server.ServeTLS(ln, "", "")
	close(done)
	<-stopped
	s.mu.Lock()
	active := s.active
	s.mu.Unlock()
	if active != 0 {
		return ErrAdmissionDrain
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// handle tracks the ACTUAL handler lifetime, not merely its connection. A forced
// connection close/cancel is not proof that a DB operation or response write ended.
func (s *AdmissionServer) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		http.Error(w, "staged admission closing; retain original input", http.StatusServiceUnavailable)
		return
	}
	s.active++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.active--
		if s.closing && s.active == 0 {
			close(s.drained)
		}
	}()
	s.handler.ServeHTTP(w, r)
}
func (s *AdmissionServer) closeAdmission() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closing {
		s.closing = true
		if s.active == 0 {
			close(s.drained)
		}
	}
}

// WaitDrained is an explicit local wait, NOT a send/retry/restart. It confirms
// admission is permanently closed AND all tracked handler calls have returned.
// On Serve's ErrAdmissionDrain, keep the shared DB/signer alive until this returns
// nil. A timeout is still unconfirmed: do not free those resources. It refuses
// before the single Serve has started, so a never-started server cannot be
// mistaken for a stopped running endpoint. It may be called while Serve stops.
func (s *AdmissionServer) WaitDrained(ctx context.Context) error {
	if s == nil || s.server == nil || s.drained == nil || ctx == nil {
		return ErrAdmissionServer
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	started, done := s.started, s.drained
	s.mu.Unlock()
	if !started {
		return ErrAdmissionServer
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
