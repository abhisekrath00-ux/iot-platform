package ingestprocessing

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAdmissionRouteRevocationFenceAndWait(t *testing.T) {
	f := httpTLSFixture(t)
	h, err := NewAdmissionHTTP(httpConfig(t, f, Engine{Enabled: true, Pool: &pgxpool.Pool{}}))
	if err != nil {
		t.Fatal(err)
	}
	leaf := sha256.Sum256(f.client.Certificate[0])
	if err := h.beginRoute(leaf); err != nil {
		t.Fatal(err)
	}
	rev, err := h.RevokeRoute(leaf)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.beginRoute(leaf); !errors.Is(err, ErrAdmissionRoute) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	if err := rev.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("active route reported drained", err)
	}
	cancel()
	h.endRoute(leaf)
	if err := rev.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	again, err := h.RevokeRoute(leaf)
	if err != nil || again.Wait(t.Context()) != nil {
		t.Fatal(err)
	}
	if _, err := h.RevokeRoute([32]byte{}); !errors.Is(err, ErrAdmissionRoute) {
		t.Fatal(err)
	}
	if _, err := (*AdmissionHTTP)(nil).RevokeRoute(leaf); !errors.Is(err, ErrAdmissionRoute) {
		t.Fatal(err)
	}
	if _, err := (&AdmissionServer{}).RevokeRoute(leaf); !errors.Is(err, ErrAdmissionRoute) {
		t.Fatal(err)
	}
	if err := (*RouteRevocation)(nil).Wait(t.Context()); !errors.Is(err, ErrAdmissionRoute) {
		t.Fatal(err)
	}
	if err := rev.Wait(nil); !errors.Is(err, ErrAdmissionRoute) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(t.Context())
	cancel()
	if err := rev.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestAdmissionRouteRevocationConcurrentBoundary(t *testing.T) {
	f := httpTLSFixture(t)
	h, err := NewAdmissionHTTP(httpConfig(t, f, Engine{Enabled: true, Pool: &pgxpool.Pool{}}))
	if err != nil {
		t.Fatal(err)
	}
	leaf := sha256.Sum256(f.client.Certificate[0])
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 30 {
		wg.Go(func() {
			<-start
			if h.beginRoute(leaf) == nil {
				h.endRoute(leaf)
			}
		})
	}
	close(start)
	rev, err := h.RevokeRoute(leaf)
	if err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if err := rev.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if h.beginRoute(leaf) == nil {
		t.Fatal("revoked route rearmed")
	}
}
func TestAdmissionRouteRevocationRealTLSBeforeDB(t *testing.T) {
	f := httpTLSFixture(t)
	s, err := NewAdmissionServer(serverConfig(t, f, Engine{Enabled: true, Pool: &pgxpool.Pool{}}))
	if err != nil {
		t.Fatal(err)
	}
	url := startAdmissionServer(t, s)
	leaf := sha256.Sum256(f.client.Certificate[0])
	rev, err := s.RevokeRoute(leaf)
	if err != nil || rev.Wait(t.Context()) != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f.roots, Certificates: []tls.Certificate{f.client}}, DisableKeepAlives: true}, Timeout: 3 * time.Second}
	body := `{"event_id":"x","device_id":"d","point_id":"p","observed_at":"2026-01-01T00:00:00Z","quality":"measured","schema_version":1,"value":1}`
	req, err := http.NewRequestWithContext(t.Context(), "POST", url+"/staged/processing-v1/admit", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-HexThings-Telemetry-Topic", "t/a/g/g/telemetry")
	// Nil underlying pgx pool would panic if this reached DB.
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 403 || strings.Contains(string(data), "signature") {
		t.Fatal(resp.StatusCode, string(data))
	}
}

func TestAdmissionRouteRevocationInFlightPG(t *testing.T) {
	x, envelope := fixture(t)
	f := httpTLSFixture(t)
	s, err := NewAdmissionServer(serverConfig(t, f, x))
	if err != nil {
		t.Fatal(err)
	}
	endpoint := startAdmissionServer(t, s)
	leaf := sha256.Sum256(f.client.Certificate[0])
	// Hold the SAME real global DB admission lock. A route call can enter before
	// revocation but cannot commit until this test explicitly releases the lock.
	blocker, err := x.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err := blocker.Exec(t.Context(), "SELECT pg_advisory_xact_lock(731492068214)"); err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f.roots, Certificates: []tls.Certificate{f.client}}, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 4 * time.Second}
	request := func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(t.Context(), "POST", endpoint+"/staged/processing-v1/admit", strings.NewReader(string(wirePayload(t, envelope))))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-HexThings-Telemetry-Topic", "t/a/g/g/telemetry")
		return client.Do(req)
	}
	type outcome struct {
		code int
		err  error
	}
	response := make(chan outcome, 1)
	go func() {
		r, e := request()
		o := outcome{err: e}
		if r != nil {
			o.code = r.StatusCode
			io.Copy(io.Discard, r.Body)
			r.Body.Close()
		}
		response <- o
	}()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	entered := false
	for !entered {
		s.admission.routeMu.Lock()
		entered = s.admission.fences[leaf].active == 1
		s.admission.routeMu.Unlock()
		if entered {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("route did not enter")
		case <-tick.C:
		}
	}
	rev, err := s.RevokeRoute(leaf)
	if err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	if err := rev.Wait(wait); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("uncommitted active route reported drained", err)
	}
	cancel()
	if err := blocker.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	original := <-response
	if original.err != nil || original.code != 200 {
		t.Fatal(original)
	}
	wait, cancel = context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := rev.Wait(wait); err != nil {
		t.Fatal(err)
	}
	counts(t, x.Pool, envelope.EventID, 1)
	historyCount(t, x, 0, "processing_started")
	r, err := request()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 403 || strings.Contains(string(data), "signature") {
		t.Fatal(r.StatusCode, string(data))
	}
	counts(t, x.Pool, envelope.EventID, 1)
	t.Log("LIVE_ROUTE_REVOCATION_FENCE_PG_COMMIT_NOT_ROLLBACK_OK")
}

func TestAdmissionRouteDenialDoesNotSurviveNewAuthorityConfig(t *testing.T) {
	f := httpTLSFixture(t)
	cfg := httpConfig(t, f, Engine{Enabled: true, Pool: &pgxpool.Pool{}})
	original, err := NewAdmissionHTTP(cfg)
	if err != nil {
		t.Fatal(err)
	}
	leaf := sha256.Sum256(f.client.Certificate[0])
	if _, err := original.RevokeRoute(leaf); err != nil {
		t.Fatal(err)
	}
	fresh, err := NewAdmissionHTTP(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately asserts the LIMITATION: a stale authority configuration can
	// reauthorize on a fresh constructor. No persistent revocation is claimed.
	if err := fresh.beginRoute(leaf); err != nil {
		t.Fatal(err)
	}
	fresh.endRoute(leaf)
	if original.beginRoute(leaf) == nil {
		t.Fatal("old instance rearmed")
	}
}
