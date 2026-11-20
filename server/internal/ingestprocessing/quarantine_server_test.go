package ingestprocessing

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func quarantineServerConfig(f httpMTLS, x Engine) GatewayQuarantineServerConfig {
	return GatewayQuarantineServerConfig{Enabled: true, Engine: x, Certificate: f.server, SessionSecret: append([]byte(nil), quarantineSecret...)}
}
func mustQuarantineServer(t *testing.T, f httpMTLS, x Engine) *GatewayQuarantineServer {
	t.Helper()
	s, err := NewGatewayQuarantineServer(t.Context(), quarantineServerConfig(f, x), filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func quarantineServerClient(t *testing.T, f httpMTLS) *http.Client {
	t.Helper()
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f.roots, MinVersion: tls.VersionTLS13}, DisableKeepAlives: true, TLSHandshakeTimeout: time.Second}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr, Timeout: 8 * time.Second}
}
func TestQuarantineServerConfigurationAndExact95(t *testing.T) {
	f := httpTLSFixture(t)
	for _, mode := range []string{"disabled", "nil-context", "certificate", "key", "mismatch", "client-leaf", "short-secret", "engine"} {
		t.Run(mode, func(t *testing.T) {
			c := quarantineServerConfig(f, Engine{Enabled: true, Pool: &pgxpool.Pool{}})
			ctx := t.Context()
			switch mode {
			case "disabled":
				c.Enabled = false
			case "nil-context":
				ctx = nil
			case "certificate":
				c.Certificate.Certificate = nil
			case "key":
				c.Certificate.PrivateKey = nil
			case "mismatch":
				c.Certificate.PrivateKey = f.client.PrivateKey
			case "client-leaf":
				c.Certificate = f.client
			case "short-secret":
				c.SessionSecret = []byte("short")
			case "engine":
				c.Engine.Enabled = false
			}
			s, err := NewGatewayQuarantineServer(ctx, c, "does-not-exist")
			if s != nil || err == nil {
				t.Fatal(s, err)
			}
		})
	}
	x, _ := fixture(t)
	if s, err := NewGatewayQuarantineServer(t.Context(), quarantineServerConfig(f, x), filepath.Join("..", "..", "migrations")); s != nil || err == nil {
		t.Fatal("exact94 accepted", s, err)
	}
	x, _ = denialFixture(t)
	c := quarantineServerConfig(f, x)
	s, err := NewGatewayQuarantineServer(t.Context(), c, filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	// Mutation cannot widen frozen server certificate metadata or shared JWT key.
	c.Certificate.Certificate[0][0] ^= 1
	for i := range c.SessionSecret {
		c.SessionSecret[i] = 0
	}
	cfg := s.transport.server.TLSConfig
	if cfg.MinVersion != tls.VersionTLS13 || cfg.ClientAuth != tls.NoClientCert || cfg.ClientCAs != nil || !cfg.SessionTicketsDisabled || len(cfg.NextProtos) != 1 || cfg.NextProtos[0] != "http/1.1" {
		t.Fatal("management TLS policy")
	}
	h := s.transport.server
	if h.ReadHeaderTimeout != 5*time.Second || h.ReadTimeout != 10*time.Second || h.WriteTimeout != 10*time.Second || h.IdleTimeout != 5*time.Second || h.MaxHeaderBytes != 8192 {
		t.Fatal("unbounded management server")
	}
	if err := s.WaitDrained(t.Context()); !errors.Is(err, ErrQuarantineServer) {
		t.Fatal("unstarted drain", err)
	}
	url := startAdmissionServer(t, s.transport)
	client := quarantineServerClient(t, f)
	r, err := client.Get(url + "/")
	if err != nil {
		t.Fatal("frozen certificate corrupted", err)
	}
	r.Body.Close()
	if r.StatusCode != 404 {
		t.Fatal(r.StatusCode)
	}
	exec(t, x.Pool, "ALTER TABLE admission_denied_leaf_v1 ADD COLUMN surprise text")
	if next, err := NewGatewayQuarantineServer(t.Context(), quarantineServerConfig(httpTLSFixture(t), x), filepath.Join("..", "..", "migrations")); next != nil || err == nil {
		t.Fatal("damaged95 accepted", next, err)
	}
	t.Log("QUARANTINE_SERVER_DEFAULT_OFF_EXACT95_FROZEN_TLS_NO_RUNTIME_OK")
}
func TestQuarantineServerActualTLSAndSlowHeaders(t *testing.T) {
	x, _ := denialFixture(t)
	f := httpTLSFixture(t)
	s := mustQuarantineServer(t, f, x)
	url := startAdmissionServer(t, s.transport)
	for _, mode := range []string{"no-client", "tls12", "h2-only", "foreign-ca"} {
		t.Run(mode, func(t *testing.T) {
			cfg := &tls.Config{RootCAs: f.roots, MinVersion: tls.VersionTLS13}
			switch mode {
			case "tls12":
				cfg.MinVersion = tls.VersionTLS12
				cfg.MaxVersion = tls.VersionTLS12
			case "h2-only":
				cfg.NextProtos = []string{"h2"}
			case "foreign-ca":
				cfg.RootCAs = httpTLSFixture(t).roots
			}
			tr := &http.Transport{TLSClientConfig: cfg, DisableKeepAlives: true, TLSHandshakeTimeout: time.Second}
			defer tr.CloseIdleConnections()
			r, err := (&http.Client{Transport: tr, Timeout: 2 * time.Second}).Post(url+"/staged/processing-v1/quarantine-gateway", "application/json", strings.NewReader("{}"))
			if mode == "no-client" {
				if err != nil {
					t.Fatal(err)
				}
				defer r.Body.Close()
				if r.StatusCode != 401 || r.ProtoMajor != 1 || r.TLS.Version != tls.VersionTLS13 {
					t.Fatal(r.StatusCode)
				}
			} else if err == nil {
				r.Body.Close()
				t.Fatal("unsafe TLS accepted")
			}
		})
	}
	dial := func() net.Conn {
		t.Helper()
		c, err := tls.Dial("tcp", strings.TrimPrefix(url, "https://"), &tls.Config{RootCAs: f.roots, MinVersion: tls.VersionTLS13})
		if err != nil {
			t.Fatal(err)
		}
		if err = c.SetDeadline(time.Now().Add(7 * time.Second)); err != nil {
			t.Fatal(err)
		}
		return c
	}
	c := dial()
	io.WriteString(c, "GET / HTTP/1.1\r\nHost: localhost\r\nX-Large: "+strings.Repeat("x", 20000)+"\r\n\r\n")
	data, err := io.ReadAll(c)
	c.Close()
	if err != nil || !strings.Contains(string(data), "431 Request Header Fields Too Large") {
		t.Fatal(string(data), err)
	}
	c = dial()
	defer c.Close()
	start := time.Now()
	io.WriteString(c, "GET / HTTP/1.1\r\nHost:")
	io.ReadAll(c)
	if time.Since(start) > 6*time.Second {
		t.Fatal("slow headers not bounded")
	}
	quarantineState(t, x, "active", 0)
	t.Log("QUARANTINE_SERVER_ACTUAL_TLS13_HTTP1_HEADERS_NO_CLIENT_CERT_AUTH_OK")
}

type quarantineCountListener struct {
	net.Listener
	accepted atomic.Int32
}

func (l *quarantineCountListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.accepted.Add(1)
	}
	return c, err
}
func TestQuarantineServerActualPreTLSCapacityAndShutdown(t *testing.T) {
	x, _ := denialFixture(t)
	f := httpTLSFixture(t)
	s := mustQuarantineServer(t, f, x)
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := &quarantineCountListener{Listener: raw}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	var clients []net.Conn
	defer func() {
		for _, c := range clients {
			c.Close()
		}
	}()
	for range admissionConnectionLimit + 1 {
		c, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, c)
	}
	deadline := time.Now().Add(2 * time.Second)
	for ln.accepted.Load() < admissionConnectionLimit && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if ln.accepted.Load() != admissionConnectionLimit {
		t.Fatal("not exactly64 accepted preTLS", ln.accepted.Load())
	}
	time.Sleep(50 * time.Millisecond)
	if ln.accepted.Load() != admissionConnectionLimit {
		t.Fatal("65th accepted before capacity released", ln.accepted.Load())
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("preTLS capacity blocked shutdown")
	}
	if err := s.WaitDrained(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.Serve(t.Context(), raw); !errors.Is(err, ErrQuarantineServer) {
		t.Fatal("reopened", err)
	}
	t.Log("QUARANTINE_SERVER_ACTUAL_64_PRETLS_65TH_BACKLOG_CANCEL_DRAIN_OK")
}

func TestQuarantineServerForcedCloseIsNotHandlerDrain(t *testing.T) {
	x, _ := denialFixture(t)
	f := httpTLSFixture(t)
	s := mustQuarantineServer(t, f, x)
	if err := (&GatewayQuarantineServer{}).WaitDrained(t.Context()); !errors.Is(err, ErrQuarantineServer) {
		t.Fatal(err)
	}
	if err := (&GatewayQuarantineServer{}).Serve(t.Context(), nil); !errors.Is(err, ErrQuarantineServer) {
		t.Fatal(err)
	}
	if err := s.WaitDrained(t.Context()); !errors.Is(err, ErrQuarantineServer) {
		t.Fatal("unstarted reported drained", err)
	}
	started, release, handlerDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s.transport.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	if err := <-done; !errors.Is(err, ErrQuarantineDrain) {
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
	s.transport.handle(rec, httptest.NewRequest("GET", "/", nil))
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
	if err := s.WaitDrained(nil); !errors.Is(err, ErrQuarantineServer) {
		t.Fatal(err)
	}
	cancelled, stop := context.WithCancel(t.Context())
	stop()
	if err := s.WaitDrained(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestQuarantineServerActualDecisionCancellationDrainsDatabaseWaiter(t *testing.T) {
	x, _ := denialFixture(t)
	f := httpTLSFixture(t)
	bindGatewayLeaf(t, x, f)
	seedQuarantineSession(t, x)
	s := mustQuarantineServer(t, f, x)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	block, err := x.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(block)
	if _, err = block.Exec(t.Context(), "UPDATE users SET role='admin' WHERE id='u'"); err != nil {
		t.Fatal(err)
	}
	client := quarantineServerClient(t, f)
	request, err := http.NewRequestWithContext(t.Context(), "POST", "https://"+ln.Addr().String()+"/staged/processing-v1/quarantine-gateway", strings.NewReader(quarantineBody(t, f)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+quarantineToken(t, "a", "u", "session", "admin"))
	request.Header.Set("Content-Type", "application/json")
	response := make(chan error, 1)
	go func() {
		r, e := client.Do(request)
		if r != nil {
			r.Body.Close()
		}
		response <- e
	}()
	waitQuarantineSQL(t, x, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'SELECT role,disabled_at%'")
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, ErrQuarantineDrain) {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("server did not close")
	}
	if err := <-response; err == nil {
		t.Fatal("blocked decision falsely acknowledged")
	}
	wait, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	if err := s.WaitDrained(wait); err != nil {
		t.Fatal("actual DB waiter did not drain", err)
	}
	if err := block.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	quarantineState(t, x, "active", 0)
	t.Log("QUARANTINE_SERVER_ACTUAL_HTTPS_DB_WAIT_CANCEL_HANDLER_DRAIN_ZERO_WRITES_OK")
}
