package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestIntegrationManualScanReservation(t *testing.T) {
	s, _ := testServer(t)
	const tenant = "itest-sreserve"
	seed(t, s, tenant)
	seed(t, s, "itest-sreserve-other")
	ctx := t.Context()
	reserve := func(id, gw string) error {
		return s.reserveManualScan(ctx, tenant, gw, id, "lan", []byte(`{"cidr":"10.0.0.0/24"}`), "synthetic")
	}
	if e := reserve("foreign", "itest-sreserve-other-gw"); !errors.Is(e, errScanGatewayMissing) {
		t.Fatal(e)
	}
	if _, e := s.st.Pool.Exec(ctx, `UPDATE gateways SET status='pending' WHERE id=$1`, tenant+"-gw"); e != nil {
		t.Fatal(e)
	}
	if e := reserve("inactive", tenant+"-gw"); !errors.Is(e, errScanGatewayInactive) {
		t.Fatal(e)
	}
	if _, e := s.st.Pool.Exec(ctx, `UPDATE gateways SET status='active' WHERE id=$1`, tenant+"-gw"); e != nil {
		t.Fatal(e)
	}
	tx, e := s.st.Pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT id FROM gateways WHERE id=$1 FOR NO KEY UPDATE`, tenant+"-gw"); e != nil {
		t.Fatal(e)
	}
	c, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if e = s.reserveManualScan(c, tenant, tenant+"-gw", "blocked", "lan", []byte(`{}`), "x"); e == nil {
		t.Fatal("bypassed gateway lock")
	}
	tx.Rollback(ctx)
	const n = 12
	var wg sync.WaitGroup
	results := make(chan error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; results <- reserve(fmt.Sprintf("sreserve-%d", i), tenant+"-gw") }(i)
	}
	close(start)
	wg.Wait()
	close(results)
	ok, blocked := 0, 0
	for e := range results {
		if e == nil {
			ok++
		} else if errors.Is(e, errScanAlreadyRunning) {
			blocked++
		} else {
			t.Fatal(e)
		}
	}
	if ok != 1 || blocked != n-1 {
		t.Fatalf("success%d blocked%d", ok, blocked)
	}
	var count int
	if e = s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM gateway_scans WHERE tenant_id=$1`, tenant).Scan(&count); e != nil || count != 1 {
		t.Fatal(e, count)
	}
	if _, e = s.st.Pool.Exec(ctx, `UPDATE gateway_scans SET created_at=now()-interval '11 minutes' WHERE tenant_id=$1`, tenant); e != nil {
		t.Fatal(e)
	}
	if e = reserve("sreserve-after-timeout", tenant+"-gw"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.st.Pool.Exec(ctx, `UPDATE gateway_scans SET status='done' WHERE tenant_id=$1`, tenant); e != nil {
		t.Fatal(e)
	}
	if e = reserve("sreserve-after-done", tenant+"-gw"); e != nil {
		t.Fatal(e)
	}
}
