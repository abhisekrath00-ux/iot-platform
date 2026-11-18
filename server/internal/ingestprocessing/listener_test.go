package ingestprocessing

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// A fake underlying listener lets the test count exact accept admissions without
// depending on the OS's separately bounded backlog or TCP dial timing.
type budgetListener struct {
	count atomic.Int32
	done  chan struct{}
	once  sync.Once
}

func (l *budgetListener) Accept() (net.Conn, error) {
	select {
	case <-l.done:
		return nil, net.ErrClosed
	default:
	}
	l.count.Add(1)
	a, b := net.Pipe()
	b.Close()
	return a, nil
}
func (l *budgetListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *budgetListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }
func TestAdmissionConnectionBudgetAndCloseUnblocks(t *testing.T) {
	raw := &budgetListener{done: make(chan struct{})}
	l := newBoundedAdmissionListener(raw)
	var conns []net.Conn
	for range admissionConnectionLimit {
		c, err := l.Accept()
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	if raw.count.Load() != admissionConnectionLimit {
		t.Fatal(raw.count.Load())
	}
	entered := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(entered)
		c, err := l.Accept()
		if c != nil {
			c.Close()
		}
		result <- err
	}()
	<-entered
	select {
	case err := <-result:
		t.Fatal("unbounded connection accept", err)
	case <-time.After(20 * time.Millisecond):
	}
	if raw.count.Load() != admissionConnectionLimit {
		t.Fatal("called underlying Accept while full")
	}
	// Closing one connection releases exactly one slot, even with repeated Close.
	conns[0].Close()
	conns[0].Close()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if raw.count.Load() != admissionConnectionLimit+1 {
		t.Fatal(raw.count.Load())
	}
	// Fill the released/reclosed slot again and test cancellation at capacity.
	c, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	conns = append(conns, c)
	go func() {
		c, err := l.Accept()
		if c != nil {
			c.Close()
		}
		result <- err
	}()
	l.Close()
	if err := <-result; !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
	for _, c := range conns {
		c.Close()
	}
	if len(l.slots) != 0 {
		t.Fatal("slot leak", len(l.slots))
	}
	if _, err := l.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
}
func TestAdmissionHandlerBudgetNoQueueAndDrain(t *testing.T) {
	f := httpTLSFixture(t)
	s, err := NewAdmissionServer(serverConfig(t, f, Engine{Enabled: true, Pool: &pgxpool.Pool{}}))
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, admissionHandlerLimit)
	release := make(chan struct{})
	var calls atomic.Int32
	s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); started <- struct{}{}; <-release })
	var wg sync.WaitGroup
	for range admissionHandlerLimit {
		wg.Go(func() { s.handle(httptest.NewRecorder(), httptest.NewRequest("POST", "/", nil)) })
	}
	for range admissionHandlerLimit {
		<-started
	}
	rejected := httptest.NewRecorder()
	s.handle(rejected, httptest.NewRequest("POST", "/", nil))
	if rejected.Code != 503 || calls.Load() != admissionHandlerLimit || rejected.Header().Get("Connection") != "close" {
		t.Fatal(rejected.Code, calls.Load())
	}
	s.closeAdmission()
	close(release)
	wg.Wait()
	select {
	case <-s.drained:
	default:
		t.Fatal("did not drain")
	}
	if calls.Load() != admissionHandlerLimit {
		t.Fatal("overload queued or retried")
	}
}
func TestAdmissionActualSlowTLSConnectionBudgetShutdown(t *testing.T) {
	f := httpTLSFixture(t)
	s, err := NewAdmissionServer(serverConfig(t, f, Engine{Enabled: true, Pool: &pgxpool.Pool{}}))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	// These clients never send a TLS handshake. They consume connection capacity
	// before HTTP and must not leave shutdown blocked on a full accept semaphore.
	var clients []net.Conn
	for range admissionConnectionLimit {
		c, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, c)
	}
	defer func() {
		for _, c := range clients {
			c.Close()
		}
	}()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("full TLS capacity prevented shutdown")
	}
	wait, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	if err := s.WaitDrained(wait); err != nil {
		t.Fatal(err)
	}
	// Listener is not reusable; a new authenticated connection cannot enter.
	c, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", ln.Addr().String(), &tls.Config{RootCAs: f.roots, Certificates: []tls.Certificate{f.client}})
	if err == nil {
		c.Close()
		t.Fatal("listener reopened")
	}
}

func TestAdmissionActualMTLSHandlerOverloadRefusesWithoutRetry(t *testing.T) {
	f := httpTLSFixture(t)
	s, err := NewAdmissionServer(serverConfig(t, f, Engine{Enabled: true, Pool: &pgxpool.Pool{}}))
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, admissionHandlerLimit)
	release := make(chan struct{})
	var calls atomic.Int32
	s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		w.WriteHeader(204)
	})
	url := startAdmissionServer(t, s)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f.roots, Certificates: []tls.Certificate{f.client}}, DisableKeepAlives: true, MaxConnsPerHost: admissionConnectionLimit}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 4 * time.Second}
	results := make(chan error, admissionHandlerLimit)
	for range admissionHandlerLimit {
		go func() {
			r, e := client.Get(url + "/")
			if r != nil {
				r.Body.Close()
				if r.StatusCode != 204 {
					e = errors.New("accepted handler response mismatch")
				}
			}
			results <- e
		}()
	}
	for range admissionHandlerLimit {
		<-started
	}
	r, err := client.Get(url + "/")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 503 || calls.Load() != admissionHandlerLimit {
		t.Fatal(r.StatusCode, calls.Load())
	}
	close(release)
	for range admissionHandlerLimit {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != admissionHandlerLimit {
		t.Fatal("overload automatically executed later")
	}
}
