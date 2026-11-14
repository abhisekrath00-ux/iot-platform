package main

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/ingestjournal"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestIntegrationAdmissionCandidateWorkStates(t *testing.T) {
	s, _ := testServer(t)
	ctx := t.Context()
	const tenant = "itest-inbox-work"
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
		if _, err := s.st.Pool.Exec(c, `DROP TABLE IF EXISTS ingest_admission_candidate`); err != nil {
			t.Error(err)
		}
	})
	j := ingestjournal.Journal{Pool: s.st.Pool}
	makeEvent := func(id string) ingestjournal.Envelope {
		e := ingestjournal.Envelope{EventID: id, TenantID: tenant, GatewayID: tenant + "-gw", DeviceID: tenant + "-dev", PointID: "temp", ObservedAt: time.Now().UTC().Truncate(time.Microsecond), Value: 42, Unit: "C", Quality: "measured", SchemaVersion: 1}
		if fresh, err := j.Admit(ctx, e); err != nil || !fresh {
			t.Fatal("admit", fresh, err)
		}
		return e
	}
	e := makeEvent("itest-work-race")
	for _, read := range []func() (ingestjournal.Work, error){func() (ingestjournal.Work, error) { return j.ReadWork(ctx, "foreign", e.EventID, e.ObservedAt) }, func() (ingestjournal.Work, error) { return j.Claim(ctx, "foreign", e.EventID, e.ObservedAt) }} {
		if _, err := read(); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal("foreign work accessible", err)
		}
	}
	if err := j.Complete(ctx, tenant, e.EventID, e.ObservedAt, uuid.NewString()); !errors.Is(err, ingestjournal.ErrState) {
		t.Fatal("pending completion accepted", err)
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	claims := make(chan ingestjournal.Work, 12)
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w, err := j.Claim(ctx, tenant, e.EventID, e.ObservedAt)
			if err == nil {
				winners.Add(1)
				claims <- w
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	close(claims)
	refused := 0
	for err := range errs {
		if errors.Is(err, ingestjournal.ErrState) {
			refused++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if winners.Load() != 1 || refused != 11 {
		t.Fatalf("claims winners%d refusals%d", winners.Load(), refused)
	}
	w := <-claims
	if w.State != "executing" || w.AttemptID == "" {
		t.Fatal("missing attempt fence", w)
	}
	if _, err := j.Pending(ctx, tenant, e.EventID, e.ObservedAt); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("claimed work still pending", err)
	}
	for _, attempt := range []string{"", uuid.NewString()} {
		if err := j.Complete(ctx, tenant, e.EventID, e.ObservedAt, attempt); !errors.Is(err, ingestjournal.ErrState) {
			t.Fatal("wrong completion fence", err)
		}
		if err := j.MarkUnknown(ctx, tenant, e.EventID, e.ObservedAt, attempt); !errors.Is(err, ingestjournal.ErrState) {
			t.Fatal("wrong quarantine fence", err)
		}
	}
	if err := j.Complete(ctx, "foreign", e.EventID, e.ObservedAt, w.AttemptID); !errors.Is(err, ingestjournal.ErrState) {
		t.Fatal("foreign completion", err)
	}
	// Competing terminal writes serialize. Exactly one can commit and no late
	// callback may alter a completed/quarantined result, even with the old token.
	winners.Store(0)
	errs = make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			if i%2 == 0 {
				err = j.Complete(ctx, tenant, e.EventID, e.ObservedAt, w.AttemptID)
			} else {
				err = j.MarkUnknown(ctx, tenant, e.EventID, e.ObservedAt, w.AttemptID)
			}
			if err == nil {
				winners.Add(1)
			}
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	refused = 0
	for err := range errs {
		if errors.Is(err, ingestjournal.ErrState) {
			refused++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if winners.Load() != 1 || refused != 11 {
		t.Fatal("terminal race", winners.Load(), refused)
	}
	reopen, err := store.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopen.Close()
	jr := ingestjournal.Journal{Pool: reopen.Pool}
	final, err := jr.ReadWork(ctx, tenant, e.EventID, e.ObservedAt)
	if err != nil || final.AttemptID != w.AttemptID || (final.State != "completed" && final.State != "outcome_unknown") {
		t.Fatal("terminal evidence", final, err)
	}
	if _, err := jr.Claim(ctx, tenant, e.EventID, e.ObservedAt); !errors.Is(err, ingestjournal.ErrState) {
		t.Fatal("terminal rearmed", err)
	}
	if _, err = s.st.Pool.Exec(ctx, `DELETE FROM telemetry WHERE event_id=$1`, e.EventID); err != nil {
		t.Fatal(err)
	}
	if fresh, err := jr.Admit(ctx, e); fresh || err != nil {
		t.Fatal("replay rearmed terminal", fresh, err)
	}
	after, err := jr.ReadWork(ctx, tenant, e.EventID, e.ObservedAt)
	if err != nil || after.State != final.State || after.AttemptID != final.AttemptID {
		t.Fatal("replay changed evidence", after, err)
	}
	// Exercise both terminal states deterministically in addition to the race.
	for _, target := range []string{"completed", "outcome_unknown"} {
		event := makeEvent("itest-work-" + target)
		attempt, err := j.Claim(ctx, tenant, event.EventID, event.ObservedAt)
		if err != nil {
			t.Fatal(err)
		}
		if target == "completed" {
			err = j.Complete(ctx, tenant, event.EventID, event.ObservedAt, attempt.AttemptID)
		} else {
			err = j.MarkUnknown(ctx, tenant, event.EventID, event.ObservedAt, attempt.AttemptID)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = j.Complete(ctx, tenant, event.EventID, event.ObservedAt, attempt.AttemptID); !errors.Is(err, ingestjournal.ErrState) {
			t.Fatal("terminal accepted late complete", err)
		}
		if err = j.MarkUnknown(ctx, tenant, event.EventID, event.ObservedAt, attempt.AttemptID); !errors.Is(err, ingestjournal.ErrState) {
			t.Fatal("terminal accepted late unknown", err)
		}
		if _, err = j.Claim(ctx, tenant, event.EventID, event.ObservedAt); !errors.Is(err, ingestjournal.ErrState) {
			t.Fatal("terminal accepted new claim", err)
		}
	}
	corrupt := makeEvent("itest-work-corrupt")
	if _, err = s.st.Pool.Exec(ctx, `UPDATE ingest_admission_candidate SET envelope=convert_to('{"tenant_id":"other"}','UTF8') WHERE event_id=$1`, corrupt.EventID); err != nil {
		t.Fatal(err)
	}
	if _, err = j.Claim(ctx, tenant, corrupt.EventID, corrupt.ObservedAt); !errors.Is(err, ingestjournal.ErrEnvelope) {
		t.Fatal("claimed corrupt envelope", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	event := makeEvent("itest-work-cancel")
	if _, err = j.Claim(cancelled, tenant, event.EventID, event.ObservedAt); err == nil {
		t.Fatal("cancelled claim succeeded")
	}
	pending, err := j.ReadWork(ctx, tenant, event.EventID, event.ObservedAt)
	if err != nil || pending.State != "pending" || pending.AttemptID != "" {
		t.Fatal("cancelled claim changed state", pending, err)
	}
	var n int
	if err = s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil || n != 93 {
		t.Fatal("ledger changed", n, err)
	}
	t.Log("UNWIRED one-attempt state model:12 concurrent claimers one winner,12 competing terminal checkpoints one winner, tenant/token/late callbacks fenced, both terminal states refuse rearm, replay and raw deletion preserve evidence; no expiry/takeover/executor or external effect")
}
