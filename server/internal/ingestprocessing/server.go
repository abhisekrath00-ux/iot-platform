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
	return &AdmissionServer{server: &http.Server{Handler: handler, TLSConfig: cfg, TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 8192}}, nil
}

// Serve runs ONCE. Cancellation permanently closes admission, drains active
// requests for up to two seconds, then closes remaining connections. A lost
// response or timeout can follow a committed transaction: retain SAME input for
// explicit reconciliation. No automatic re-admission/effect retry is performed.
// Listener ownership transfers only once serving starts, including on errors.
// Cancelled/nil/reused calls refuse before taking ownership of a new listener.
func (s *AdmissionServer) Serve(ctx context.Context, ln net.Listener) error {
	if s == nil || ctx == nil || ln == nil {
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
			drain, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := s.server.Shutdown(drain); err != nil {
				_ = s.server.Close()
			}
		case <-done:
		}
	}()
	err := s.server.ServeTLS(ln, "", "")
	close(done)
	<-stopped
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
