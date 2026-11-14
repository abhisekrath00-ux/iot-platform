package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/ingestjournal"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
	"github.com/jackc/pgx/v5"
)

// Candidate schema is installed ONLY in this disposable TEST_DATABASE_URL
// fixture. Product migration ledger and compiled ingest remain unchanged.
func TestIntegrationAdmissionCandidate(t *testing.T) {
	s, _ := testServer(t)
	ctx := t.Context()
	const tenant = "itest-inbox-candidate"
	seed(t, s, tenant)
	schema, err := os.ReadFile("../../internal/ingestjournal/candidate.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.st.Pool.Exec(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := s.st.Pool.Exec(c, `DROP TABLE IF EXISTS ingest_admission_candidate; DROP FUNCTION IF EXISTS itest_admission_candidate_fault()`); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	j := ingestjournal.Journal{Pool: s.st.Pool}
	base := ingestjournal.Envelope{EventID: "itest-inbox-1", TenantID: tenant, GatewayID: tenant + "-gw", DeviceID: tenant + "-dev", PointID: "temp", ObservedAt: time.Now().UTC().Truncate(time.Microsecond).Add(123 * time.Nanosecond), Value: 42, Unit: "C", Quality: "measured", SchemaVersion: 1}
	admit := func(e ingestjournal.Envelope, want bool, werr error) {
		t.Helper()
		got, err := j.Admit(ctx, e)
		if got != want || !errors.Is(err, werr) {
			t.Fatalf("admit%s got%v err%v want%v/%v", e.EventID, got, err, want, werr)
		}
	}
	admit(base, true, nil)
	admit(base, false, nil)
	same := base
	same.ObservedAt = base.ObservedAt.In(time.FixedZone("offset", 19800)).Add(100 * time.Nanosecond)
	admit(same, false, nil)
	changed := base
	changed.Value = 43
	admit(changed, false, ingestjournal.ErrConflict)
	foreign := base
	foreign.TenantID = "other"
	admit(foreign, false, ingestjournal.ErrConflict)
	if _, err = j.Pending(ctx, "other", base.EventID, base.ObservedAt); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("tenant-scoped read", err)
	}
	read, err := j.Pending(ctx, tenant, base.EventID, base.ObservedAt)
	if err != nil || read.Value != 42 || read.ObservedAt.Nanosecond()%1000 != 0 {
		t.Fatal("canonical original", read, err)
	}
	// Fresh pool verifies independent connection access, not process restart.
	reopen, err := store.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopen.Close()
	jr := ingestjournal.Journal{Pool: reopen.Pool}
	if fresh, err := jr.Admit(ctx, base); err != nil || fresh {
		t.Fatal("reopen", fresh, err)
	}
	concurrent := base
	concurrent.EventID = "itest-inbox-concurrent"
	var winners atomic.Int32
	errs := make(chan error, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fresh, err := jr.Admit(ctx, concurrent)
			if fresh {
				winners.Add(1)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if winners.Load() != 1 {
		t.Fatal("concurrent winners", winners.Load())
	}
	// A second write failure must roll back the telemetry INSERT in the same tx.
	for _, q := range []string{`CREATE FUNCTION itest_admission_candidate_fault() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic pending insert refusal'; END$$`, `CREATE TRIGGER itest_admission_candidate_fault BEFORE INSERT ON ingest_admission_candidate FOR EACH ROW EXECUTE FUNCTION itest_admission_candidate_fault()`} {
		if _, err = s.st.Pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	failed := base
	failed.EventID = "itest-inbox-fault"
	if fresh, err := j.Admit(ctx, failed); err == nil || fresh {
		t.Fatal("journal fault accepted", fresh, err)
	}
	var n int
	if err = s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM telemetry WHERE event_id=$1`, failed.EventID).Scan(&n); err != nil || n != 0 {
		t.Fatal("partial telemetry commit", n, err)
	}
	if _, err = s.st.Pool.Exec(ctx, `DROP TRIGGER itest_admission_candidate_fault ON ingest_admission_candidate; DROP FUNCTION itest_admission_candidate_fault()`); err != nil {
		t.Fatal(err)
	}
	admit(failed, true, nil)
	legacy := base
	legacy.EventID = "itest-inbox-legacy"
	if _, err = s.st.Pool.Exec(ctx, `INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,value,unit,schema_version)VALUES($1,$2,$3,$4,'temp',$5,42,'C',1)`, legacy.EventID, tenant, legacy.GatewayID, legacy.DeviceID, legacy.ObservedAt); err != nil {
		t.Fatal(err)
	}
	admit(legacy, false, ingestjournal.ErrLegacy)
	// Retention of raw telemetry does not erase journal evidence or rearm work.
	if _, err = s.st.Pool.Exec(ctx, `DELETE FROM telemetry WHERE event_id=$1`, base.EventID); err != nil {
		t.Fatal(err)
	}
	admit(base, false, nil)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	invalid := base
	invalid.EventID = "itest-inbox-cancel"
	if fresh, err := j.Admit(cancelled, invalid); fresh || err == nil {
		t.Fatal("cancelled admit", fresh, err)
	}
	// Populate only inert candidate rows to exercise the exact global cap.
	if _, err = s.st.Pool.Exec(ctx, `INSERT INTO ingest_admission_candidate(event_id,observed_at,tenant_id,envelope) SELECT 'capacity-'||g,now(),'capacity',decode('00','hex') FROM generate_series(1,$1::int) g`, ingestjournal.Capacity-4); err != nil {
		t.Fatal(err)
	}
	winners.Store(0)
	capErrs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e := base
			e.EventID = fmt.Sprintf("itest-inbox-last-slot-%d", i)
			fresh, err := j.Admit(ctx, e)
			if fresh {
				winners.Add(1)
			}
			capErrs <- err
		}(i)
	}
	wg.Wait()
	close(capErrs)
	refused := 0
	for err := range capErrs {
		if errors.Is(err, ingestjournal.ErrCapacity) {
			refused++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if winners.Load() != 1 || refused != 11 {
		t.Fatalf("capacity race winners%d refusals%d", winners.Load(), refused)
	}
	capped := base
	capped.EventID = "itest-inbox-cap"
	admit(capped, false, ingestjournal.ErrCapacity)
	admit(failed, false, nil)
	if err = s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM telemetry WHERE event_id=$1`, capped.EventID).Scan(&n); err != nil || n != 0 {
		t.Fatal("capacity leaked telemetry", n, err)
	}
	// Corrupted evidence must fail a tenant-scoped read rather than disclose
	// a forged envelope stored under the requested record's routing columns.
	if _, err = s.st.Pool.Exec(ctx, `UPDATE ingest_admission_candidate SET envelope=convert_to('{"tenant_id":"other"}','UTF8') WHERE event_id=$1`, failed.EventID); err != nil {
		t.Fatal(err)
	}
	if _, err = j.Pending(ctx, tenant, failed.EventID, failed.ObservedAt); !errors.Is(err, ingestjournal.ErrEnvelope) {
		t.Fatal("corrupt pending read", err)
	}
	if _, err = s.st.Pool.Exec(ctx, `DROP TABLE ingest_admission_candidate`); err != nil {
		t.Fatal(err)
	}
	missing := base
	missing.EventID = "itest-inbox-table-missing"
	if fresh, err := j.Admit(ctx, missing); fresh || err == nil {
		t.Fatal("missing table admitted", fresh, err)
	}
	if err = s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM telemetry WHERE event_id=$1`, missing.EventID).Scan(&n); err != nil || n != 0 {
		t.Fatal("missing table leaked row", n, err)
	}
	var applied int
	if err = s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&applied); err != nil || applied != 93 {
		t.Fatal("candidate changed migration ledger", applied, err)
	}
	t.Log(fmt.Sprintf("UNWIRED admission candidate: atomic reading+pending intent, exact replay/payload and tenant conflict, microsecond key, fresh-pool reopen,12concurrent one winner, second-write rollback, legacy refusal, raw-retention replay suppression, cancel and%d hard cap; migration93 unchanged. No MQTT/worker/effect recovery acceptance", ingestjournal.Capacity))
}
