package ingestprocessing

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func serverConfig(t *testing.T, f httpMTLS, x Engine) AdmissionServerConfig {
	return AdmissionServerConfig{Enabled: true, Admission: httpConfig(t, f, x), Certificate: f.server, ClientCAs: f.roots}
}
func startAdmissionServer(t *testing.T, s *AdmissionServer) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(4 * time.Second):
			t.Error("server failed to drain")
		}
	})
	return "https://" + ln.Addr().String()
}
func TestAdmissionServerConfigAndFrozenTLS(t *testing.T) {
	for _, mode := range []string{"disabled", "roots", "empty-roots", "certificate", "key", "mismatch", "client-leaf", "handler"} {
		t.Run(mode, func(t *testing.T) {
			f := httpTLSFixture(t)
			c := serverConfig(t, f, Engine{Enabled: true, Pool: &pgxpool.Pool{}})
			switch mode {
			case "disabled":
				c.Enabled = false
			case "roots":
				c.ClientCAs = nil
			case "empty-roots":
				c.ClientCAs = x509.NewCertPool()
			case "certificate":
				c.Certificate.Certificate = nil
			case "key":
				c.Certificate.PrivateKey = nil
			case "mismatch":
				c.Certificate.PrivateKey = f.client.PrivateKey
			case "client-leaf":
				c.Certificate = f.client
			case "handler":
				c.Admission.Enabled = false
			}
			if s, err := NewAdmissionServer(c); s != nil || err == nil {
				t.Fatal(s, err)
			}
		})
	}
	f := httpTLSFixture(t)
	c := serverConfig(t, f, Engine{Enabled: true, Pool: &pgxpool.Pool{}})
	s, err := NewAdmissionServer(c)
	if err != nil {
		t.Fatal(err)
	}
	c.Certificate.Certificate[0][0] ^= 1
	c.Admission.Routes[0].Topic = "t/foreign/g/g/telemetry"
	cfg := s.server.TLSConfig
	if cfg.MinVersion != tls.VersionTLS13 || cfg.ClientAuth != tls.RequireAndVerifyClientCert || !cfg.SessionTicketsDisabled || len(cfg.NextProtos) != 1 || cfg.NextProtos[0] != "http/1.1" || s.server.MaxHeaderBytes != 8192 || s.server.ReadHeaderTimeout != 5*time.Second || s.server.ReadTimeout != 30*time.Second || s.server.WriteTimeout != 30*time.Second || s.server.IdleTimeout != 5*time.Second {
		t.Fatal("unbounded or insecure transport")
	}
	if _, err := x509.ParseCertificate(cfg.Certificates[0].Certificate[0]); err != nil {
		t.Fatal("caller mutated frozen leaf", err)
	}
	if !bytes.Equal(cfg.Certificates[0].Leaf.Raw, cfg.Certificates[0].Certificate[0]) {
		t.Fatal("caller mutated cached leaf")
	}
	if s.started {
		t.Fatal("constructor started listener")
	}
}
func TestAdmissionServerActualTLSRefusalAndShutdown(t *testing.T) {
	f := httpTLSFixture(t)
	s, err := NewAdmissionServer(serverConfig(t, f, Engine{Enabled: true, Pool: &pgxpool.Pool{}}))
	if err != nil {
		t.Fatal(err)
	}
	url := startAdmissionServer(t, s)
	for _, mode := range []string{"valid-no-db", "missing-client", "unknown-client", "tls12", "h2-only", "foreign-ca"} {
		t.Run(mode, func(t *testing.T) {
			cfg := &tls.Config{RootCAs: f.roots, Certificates: []tls.Certificate{f.client}, MinVersion: tls.VersionTLS13}
			switch mode {
			case "missing-client":
				cfg.Certificates = nil
			case "unknown-client":
				cfg.Certificates = []tls.Certificate{f.foreign}
			case "tls12":
				cfg.MinVersion = tls.VersionTLS12
				cfg.MaxVersion = tls.VersionTLS12
			case "h2-only":
				cfg.NextProtos = []string{"h2"}
			case "foreign-ca":
				cfg.RootCAs = httpTLSFixture(t).roots
			}
			transport := &http.Transport{TLSClientConfig: cfg, DisableKeepAlives: true, TLSHandshakeTimeout: time.Second}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
			// An invalid method safely exercises real TLS without any DB operation.
			resp, err := client.Get(url + "/staged/processing-v1/admit")
			if mode == "valid-no-db" || mode == "unknown-client" {
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != 400 || resp.TLS.Version != tls.VersionTLS13 || resp.ProtoMajor != 1 {
					t.Fatal(resp.StatusCode, resp.Proto)
				}
			} else if err == nil {
				resp.Body.Close()
				t.Fatal("unsafe handshake accepted")
			}
		})
	}
	// Actual oversized HTTP headers refuse before any DB operation.
	conn, err := tls.Dial("tcp", strings.TrimPrefix(url, "https://"), &tls.Config{RootCAs: f.roots, Certificates: []tls.Certificate{f.client}, MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	io.WriteString(conn, "GET / HTTP/1.1\r\nHost: localhost\r\nX-Large: "+strings.Repeat("x", 20000)+"\r\n\r\n")
	data, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "431 Request Header Fields Too Large") {
		t.Fatal(string(data))
	}
}
func TestAdmissionServerCancelledAndSingleUse(t *testing.T) {
	f := httpTLSFixture(t)
	s, err := NewAdmissionServer(serverConfig(t, f, Engine{Enabled: true, Pool: &pgxpool.Pool{}}))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.Serve(ctx, ln); !errors.Is(err, context.Canceled) || s.started {
		t.Fatal(err, s.started)
	}
	if err := s.Serve(nil, ln); !errors.Is(err, ErrAdmissionServer) {
		t.Fatal(err)
	}
	if err := s.Serve(t.Context(), nil); !errors.Is(err, ErrAdmissionServer) {
		t.Fatal(err)
	}
	// A closed listener ends the single attempt and does not authorize reopening.
	ln.Close()
	if err := s.Serve(t.Context(), ln); err == nil {
		t.Fatal("closed listener accepted")
	}
	next, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if err := s.Serve(t.Context(), next); !errors.Is(err, ErrAdmissionServer) {
		t.Fatal(err)
	}
}

func TestAdmissionServerCancellationDrainsInFlightWithoutRetry(t *testing.T) {
	f := httpTLSFixture(t)
	s, err := NewAdmissionServer(serverConfig(t, f, Engine{Enabled: true, Pool: &pgxpool.Pool{}}))
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	calls := 0
	// Test-only block replaces the handler, not the product TLS/Serve lifecycle.
	s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; close(started); <-release; w.WriteHeader(204) })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	response := make(chan error, 1)
	go func() {
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f.roots, Certificates: []tls.Certificate{f.client}}, DisableKeepAlives: true}, Timeout: 3 * time.Second}
		r, e := client.Get("https://" + ln.Addr().String() + "/")
		if r != nil {
			r.Body.Close()
		}
		response <- e
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		t.Fatal("returned before active request drained", err)
	default:
	}
	close(release)
	if err := <-response; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("automatic handler retry", calls)
	}
	if c, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second); err == nil {
		c.Close()
		t.Fatal("listener remained open")
	}
}

func TestAdmissionServerFrozenClientRoots(t *testing.T) {
	f, other := httpTLSFixture(t), httpTLSFixture(t)
	s, err := NewAdmissionServer(serverConfig(t, f, Engine{Enabled: true, Pool: &pgxpool.Pool{}}))
	if err != nil {
		t.Fatal(err)
	}
	f.roots.AddCert(mustCert(t, other.ca))
	url := startAdmissionServer(t, s)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f.roots, Certificates: []tls.Certificate{other.client}}, DisableKeepAlives: true}, Timeout: 2 * time.Second}
	r, err := client.Get(url + "/")
	if r != nil {
		r.Body.Close()
	}
	if err == nil {
		t.Fatal("caller widened frozen client trust")
	}
}

func TestAdmissionServerActualUnknownRouteAndSlowHeaders(t *testing.T) {
	t.Parallel() // Independent ephemeral listeners and, when used, a disposable DB.
	f := httpTLSFixture(t)
	s, err := NewAdmissionServer(serverConfig(t, f, Engine{Enabled: true, Pool: &pgxpool.Pool{}}))
	if err != nil {
		t.Fatal(err)
	}
	url := startAdmissionServer(t, s)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f.roots, Certificates: []tls.Certificate{f.foreign}}, DisableKeepAlives: true}, Timeout: 2 * time.Second}
	request, err := http.NewRequestWithContext(t.Context(), "POST", url+"/staged/processing-v1/admit", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-HexThings-Telemetry-Topic", "t/a/g/g/telemetry")
	r, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 403 {
		t.Fatal("unknown leaf reached DB", r.StatusCode)
	}
	conn, err := tls.Dial("tcp", strings.TrimPrefix(url, "https://"), &tls.Config{RootCAs: f.roots, Certificates: []tls.Certificate{f.client}, MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(7 * time.Second))
	start := time.Now()
	io.WriteString(conn, "GET / HTTP/1.1\r\nHost:")
	_, err = io.ReadAll(conn)
	if time.Since(start) > 6*time.Second {
		t.Fatal("header timeout not enforced", err)
	}
}

func TestAdmissionServerForcedCloseIsNotHandlerDrain(t *testing.T) {
	t.Parallel() // Independent ephemeral listener and test-only handler.
	f := httpTLSFixture(t)
	s, err := NewAdmissionServer(serverConfig(t, f, Engine{Enabled: true, Pool: &pgxpool.Pool{}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := (&AdmissionServer{}).WaitDrained(t.Context()); !errors.Is(err, ErrAdmissionServer) {
		t.Fatal(err)
	}
	if err := (&AdmissionServer{}).Serve(t.Context(), nil); !errors.Is(err, ErrAdmissionServer) {
		t.Fatal(err)
	}
	if err := s.WaitDrained(t.Context()); !errors.Is(err, ErrAdmissionServer) {
		t.Fatal("unstarted reported drained", err)
	}
	started, release, handlerDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(started)
		<-release
		close(handlerDone)
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	response := make(chan error, 1)
	go func() {
		transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f.roots, Certificates: []tls.Certificate{f.client}}, DisableKeepAlives: true}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 4 * time.Second}
		r, e := client.Get("https://" + ln.Addr().String() + "/")
		if r != nil {
			r.Body.Close()
		}
		response <- e
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, ErrAdmissionDrain) {
		t.Fatal("forced connection close falsely confirmed drain", err)
	}
	if err := <-response; err == nil {
		t.Fatal("uncompleted response succeeded")
	}
	wait, stop := context.WithTimeout(t.Context(), 20*time.Millisecond)
	if err := s.WaitDrained(wait); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("active handler reported drained", err)
	}
	stop()
	// No new handler may enter once shutdown begins, including direct dispatch.
	rec := httptest.NewRecorder()
	s.handle(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 503 || calls.Load() != 1 {
		t.Fatal(rec.Code, calls.Load())
	}
	close(release)
	<-handlerDone
	wait, stop = context.WithTimeout(t.Context(), time.Second)
	defer stop()
	if err := s.WaitDrained(wait); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("automatic retry", calls.Load())
	}
	if err := s.WaitDrained(nil); !errors.Is(err, ErrAdmissionServer) {
		t.Fatal(err)
	}
	cancelled, stop := context.WithCancel(t.Context())
	stop()
	if err := s.WaitDrained(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
