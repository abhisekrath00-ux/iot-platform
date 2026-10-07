package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// reportGate bounds how many reports one API process builds at once, overall and per tenant, so a burst
// of previews, downloads or scheduled runs cannot exhaust CPU, memory or the database pool. A caller that
// cannot get a slot within the wait gets errReportBusy (HTTP 503 with Retry-After) instead of queueing
// without limit. Limits are per process: with N replicas the cluster total is N times the setting.
type reportGate struct {
	mu        sync.Mutex
	global    chan struct{}
	perTenant int
	active    map[string]int
	wait      time.Duration
}

var errReportBusy = errors.New("report capacity busy, retry shortly")

func envInt(name string, def, lo, hi int) int {
	v, err := strconv.Atoi(os.Getenv(name))
	if err != nil || v < lo || v > hi {
		return def
	}
	return v
}

func newReportGate(global, perTenant int, wait time.Duration) *reportGate {
	if global < 1 {
		global = 1
	}
	if perTenant < 1 {
		perTenant = 1
	}
	return &reportGate{global: make(chan struct{}, global), perTenant: perTenant, active: map[string]int{}, wait: wait}
}

func reportGateFromEnv() *reportGate {
	return newReportGate(envInt("REPORT_MAX_CONCURRENT", 4, 1, 256), envInt("REPORT_MAX_PER_TENANT", 2, 1, 256),
		time.Duration(envInt("REPORT_QUEUE_WAIT_SECONDS", 10, 0, 120))*time.Second)
}

// acquire returns a release func. The release is safe to call once; call it with defer.
func (g *reportGate) acquire(ctx context.Context, tenant string) (func(), error) {
	g.mu.Lock()
	if g.active[tenant] >= g.perTenant {
		g.mu.Unlock()
		return nil, errReportBusy
	}
	g.active[tenant]++
	g.mu.Unlock()
	undo := func() {
		g.mu.Lock()
		if g.active[tenant]--; g.active[tenant] <= 0 {
			delete(g.active, tenant)
		}
		g.mu.Unlock()
	}
	t := time.NewTimer(g.wait)
	defer t.Stop()
	select {
	case g.global <- struct{}{}:
	case <-t.C:
		undo()
		return nil, errReportBusy
	case <-ctx.Done():
		undo()
		return nil, ctx.Err()
	}
	var once sync.Once
	return func() { once.Do(func() { <-g.global; undo() }) }, nil
}

var reports = reportGateFromEnv()

func writeReportBusy(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "5")
	http.Error(w, errReportBusy.Error(), http.StatusServiceUnavailable)
}

// errReportTooLarge is returned when a report would read more bucket rows than REPORT_MAX_ROWS
// (default 200000 across all metrics). It stops one request from building a huge HTML/PDF in memory.
var errReportTooLarge = errors.New("report too large: narrow the window, use a coarser group_by, or select fewer metrics")

func reportRowCap() int {
	if v, err := strconv.Atoi(os.Getenv("REPORT_MAX_ROWS")); err == nil && v > 0 {
		return v
	}
	return 200000
}

func checkRowCap(total int) error {
	if total > reportRowCap() {
		return errReportTooLarge
	}
	return nil
}

func reportErrStatus(err error) int {
	if errors.Is(err, errReportTooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusInternalServerError
}
