package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestReportGateGlobalCapAndRelease(t *testing.T) {
	g := newReportGate(2, 5, 50*time.Millisecond)
	r1, e1 := g.acquire(context.Background(), "a")
	r2, e2 := g.acquire(context.Background(), "b")
	if e1 != nil || e2 != nil {
		t.Fatal(e1, e2)
	}
	if _, err := g.acquire(context.Background(), "c"); !errors.Is(err, errReportBusy) {
		t.Fatalf("want busy, got %v", err)
	}
	r1()
	r1() // double release must not free a second slot
	r3, err := g.acquire(context.Background(), "c")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.acquire(context.Background(), "d"); !errors.Is(err, errReportBusy) {
		t.Fatalf("double release leaked a slot: %v", err)
	}
	r2()
	r3()
	if len(g.active) != 0 {
		t.Fatalf("tenant counters leaked: %v", g.active)
	}
}

func TestReportGatePerTenantFairness(t *testing.T) {
	g := newReportGate(10, 1, time.Second)
	r, _ := g.acquire(context.Background(), "noisy")
	if _, err := g.acquire(context.Background(), "noisy"); !errors.Is(err, errReportBusy) {
		t.Fatal("second slot for the same tenant must be refused immediately")
	}
	other, err := g.acquire(context.Background(), "quiet")
	if err != nil {
		t.Fatalf("quiet tenant starved: %v", err)
	}
	other()
	r()
}

func TestReportGateWaitsThenGetsSlotAndHonoursCancel(t *testing.T) {
	g := newReportGate(1, 5, time.Second)
	r, _ := g.acquire(context.Background(), "a")
	go func() { time.Sleep(30 * time.Millisecond); r() }()
	r2, err := g.acquire(context.Background(), "b")
	if err != nil {
		t.Fatalf("should have waited for the slot: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.acquire(ctx, "c"); !errors.Is(err, context.Canceled) {
		t.Fatalf("want canceled, got %v", err)
	}
	r2()
	if len(g.active) != 0 {
		t.Fatalf("leak after cancel: %v", g.active)
	}
}

func TestReportGateConcurrentNeverExceedsCap(t *testing.T) {
	g := newReportGate(3, 100, 2*time.Second)
	var mu sync.Mutex
	cur, peak := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rel, err := g.acquire(context.Background(), "t")
			if err != nil {
				return
			}
			mu.Lock()
			cur++
			if cur > peak {
				peak = cur
			}
			mu.Unlock()
			time.Sleep(2 * time.Millisecond)
			mu.Lock()
			cur--
			mu.Unlock()
			rel()
		}()
	}
	wg.Wait()
	if peak > 3 {
		t.Fatalf("peak %d exceeded cap 3", peak)
	}
}

func TestEnvIntBounds(t *testing.T) {
	t.Setenv("X_T", "abc")
	if envInt("X_T", 4, 1, 10) != 4 {
		t.Fatal("bad value must fall back")
	}
	t.Setenv("X_T", "99")
	if envInt("X_T", 4, 1, 10) != 4 {
		t.Fatal("out of range must fall back")
	}
	t.Setenv("X_T", "7")
	if envInt("X_T", 4, 1, 10) != 7 {
		t.Fatal("valid value")
	}
}

// With every slot taken, preview and run answer 503 with Retry-After and a retry succeeds once a slot frees.
func TestReportPreviewBusyReturns503(t *testing.T) {
	_, h := testServer(t)
	old := reports
	reports = newReportGate(1, 1, 20*time.Millisecond)
	defer func() { reports = old }()
	hold, err := reports.acquire(context.Background(), "itest-gate")
	if err != nil {
		t.Fatal(err)
	}
	def := `{"metrics":[{"device_id":"itest-gate-dev","point_id":"temp"}],"window_hours":1,"group_by":"hour"}`
	w := call(h, "itest-gate", "viewer", "POST", "/v1/reports/preview", `{"name":"P","definition":`+def+`}`)
	if w.Code != 503 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("want 503 + Retry-After, got %d %v", w.Code, w.Header())
	}
	hold()
	w = call(h, "itest-gate", "viewer", "POST", "/v1/reports/preview", `{"name":"P","definition":`+def+`}`)
	if w.Code != 200 {
		t.Fatalf("after release want 200, got %d %s", w.Code, w.Body.String())
	}
}
