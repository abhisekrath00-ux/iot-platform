package main

import (
	"bytes"
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

func TestIntegrationFlowEvidenceBindingCandidate(t *testing.T) {
	s, _ := testServer(t)
	ctx := t.Context()
	tenant := "itest-flow-binding"
	seed(t, s, tenant)
	for _, path := range []string{"candidate.sql", "flow_binding_candidate.sql"} {
		b, err := os.ReadFile("../../internal/ingestjournal/" + path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.st.Pool.Exec(ctx, string(b)); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := s.st.Pool.Exec(c, `DROP TABLE IF EXISTS ingest_flow_evidence_candidate;DROP TABLE IF EXISTS ingest_admission_candidate;DROP FUNCTION IF EXISTS itest_flow_bind_fault()`); err != nil {
			t.Error(err)
		}
	})
	e := ingestjournal.Envelope{EventID: "flow-bind-event", TenantID: tenant, GatewayID: tenant + "-gw", DeviceID: tenant + "-dev", PointID: "temp", ObservedAt: time.Now().UTC().Truncate(time.Microsecond), Value: 42, Quality: "measured", SchemaVersion: 1}
	id := tenant + "-flow"
	if _, err := s.st.Pool.Exec(ctx, `UPDATE flows SET published_version_id=NULL WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.st.Pool.Exec(ctx, `DELETE FROM flows WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	def := `{"trigger":{"device_id":"` + e.DeviceID + `","point_id":"temp","op":">","value":0},"steps":[{"type":"notify","channel_id":"disabled-fixture","message":"synthetic"}]}`
	if _, err := s.st.Pool.Exec(ctx, `INSERT INTO flows(id,tenant_id,name,definition,created_by) VALUES($1,$2,'Original',$3,'test-user')`, id, tenant, def); err != nil {
		t.Fatal(err)
	}
	if _, err := s.st.Pool.Exec(ctx, `INSERT INTO flow_versions(id,flow_id,tenant_id,version,definition,status,created_by) VALUES($1,$2,$3,1,$4,'published','test-user')`, id+"-v1", id, tenant, def); err != nil {
		t.Fatal(err)
	}
	if _, err := s.st.Pool.Exec(ctx, `UPDATE flows SET published_version_id=$2 WHERE id=$1`, id, id+"-v1"); err != nil {
		t.Fatal(err)
	}
	j := ingestjournal.Journal{Pool: s.st.Pool}
	capture := func(e ingestjournal.Envelope) ([]byte, string) {
		t.Helper()
		b, h, err := j.CapturePublishedFlow(ctx, e, id)
		if err != nil {
			t.Fatal(err)
		}
		return b, h
	}
	b, h := capture(e)
	if _, err := j.BindFlowEvidence(ctx, e, b, h); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("bound unadmitted event", err)
	}
	if fresh, err := j.Admit(ctx, e); err != nil || !fresh {
		t.Fatal(fresh, err)
	}
	var winners atomic.Int32
	errs := make(chan error, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fresh, err := j.BindFlowEvidence(ctx, e, b, h)
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
		t.Fatal("binding winners", winners.Load())
	}
	reopen, err := store.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopen.Close()
	jr := ingestjournal.Journal{Pool: reopen.Pool}
	got, gh, err := jr.ReadFlowEvidence(ctx, tenant, e.EventID, e.ObservedAt)
	if err != nil || !bytes.Equal(got, b) || gh != h {
		t.Fatal("retained reopen", err)
	}
	foreign := e
	foreign.TenantID = "foreign"
	if _, _, err := jr.ReadFlowEvidence(ctx, foreign.TenantID, e.EventID, e.ObservedAt); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("foreign read", err)
	}
	changed := e
	changed.Value++
	cb, ch := capture(changed)
	if _, err := j.BindFlowEvidence(ctx, changed, cb, ch); !errors.Is(err, ingestjournal.ErrConflict) {
		t.Fatal("changed input", err)
	}
	if _, err := s.st.Pool.Exec(ctx, `UPDATE flows SET name='Changed' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	newB, newH := capture(e)
	if _, err := j.BindFlowEvidence(ctx, e, newB, newH); !errors.Is(err, ingestjournal.ErrConflict) {
		t.Fatal("replaced selected config", err)
	}
	w, err := j.Claim(ctx, tenant, e.EventID, e.ObservedAt)
	if err != nil {
		t.Fatal(err)
	}
	if fresh, err := j.BindFlowEvidence(ctx, e, b, h); err != nil || fresh {
		t.Fatal("exact replay should inspect only", fresh, err)
	}
	if err := j.MarkUnknown(ctx, tenant, e.EventID, e.ObservedAt, w.AttemptID); err != nil {
		t.Fatal(err)
	}
	if fresh, err := j.BindFlowEvidence(ctx, e, b, h); err != nil || fresh {
		t.Fatal("terminal replay rearmed", fresh, err)
	}
	// Claim still works WITHOUT a binding: no runtime or Claim gate was enabled.
	unbound := e
	unbound.EventID = "flow-bind-already-executing"
	if _, err := j.Admit(ctx, unbound); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Claim(ctx, tenant, unbound.EventID, unbound.ObservedAt); err != nil {
		t.Fatal(err)
	}
	ub, uh := capture(unbound)
	if _, err := j.BindFlowEvidence(ctx, unbound, ub, uh); !errors.Is(err, ingestjournal.ErrState) {
		t.Fatal("late binding", err)
	}
	// Bind and Claim share the admission row lock. Either ordering is safe:
	// retained-before-Claim or Claim-before-refused-binding, never a late insert.
	for i := 0; i < 20; i++ {
		race := e
		race.EventID = fmt.Sprintf("flow-bind-claim-race-%d", i)
		if _, err := j.Admit(ctx, race); err != nil {
			t.Fatal(err)
		}
		rb, rh := capture(race)
		start := make(chan struct{})
		type bindingResult struct {
			fresh bool
			err   error
		}
		bound := make(chan bindingResult, 1)
		claimed := make(chan error, 1)
		go func() {
			<-start
			fresh, err := j.BindFlowEvidence(ctx, race, rb, rh)
			bound <- bindingResult{fresh, err}
		}()
		go func() { <-start; _, err := j.Claim(ctx, tenant, race.EventID, race.ObservedAt); claimed <- err }()
		close(start)
		br, ce := <-bound, <-claimed
		if ce != nil {
			t.Fatal("racing Claim", ce)
		}
		if br.err != nil && !errors.Is(br.err, ingestjournal.ErrState) {
			t.Fatal("racing binding", br.err)
		}
		retained, digest, re := j.ReadFlowEvidence(ctx, tenant, race.EventID, race.ObservedAt)
		if br.fresh {
			if br.err != nil || re != nil || !bytes.Equal(retained, rb) || digest != rh {
				t.Fatal("lost winning binding", br.err, re)
			}
		} else if !errors.Is(br.err, ingestjournal.ErrState) || !errors.Is(re, pgx.ErrNoRows) {
			t.Fatal("late binding retained", br.err, re)
		}
		if fresh, err := j.BindFlowEvidence(ctx, race, rb, rh); fresh || (br.fresh && err != nil) || (!br.fresh && !errors.Is(err, ingestjournal.ErrState)) {
			t.Fatal("racing replay", fresh, err)
		}
		if w, err := j.ReadWork(ctx, tenant, race.EventID, race.ObservedAt); err != nil || w.State != "executing" {
			t.Fatal("binding changed Claim", w, err)
		}
	}
	// Storage failure cannot leave half evidence or alter the admission.
	fault := e
	fault.EventID = "flow-bind-storage-fault"
	if _, err := j.Admit(ctx, fault); err != nil {
		t.Fatal(err)
	}
	fb, fh := capture(fault)
	if _, err := s.st.Pool.Exec(ctx, `CREATE FUNCTION itest_flow_bind_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic evidence refusal'; END $$;CREATE TRIGGER itest_bind_fault BEFORE INSERT ON ingest_flow_evidence_candidate FOR EACH ROW EXECUTE FUNCTION itest_flow_bind_fault()`); err != nil {
		t.Fatal(err)
	}
	if _, err := j.BindFlowEvidence(ctx, fault, fb, fh); err == nil {
		t.Fatal("storage failure ignored")
	}
	if _, _, err := j.ReadFlowEvidence(ctx, tenant, fault.EventID, fault.ObservedAt); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("partial binding", err)
	}
	if w, err := j.ReadWork(ctx, tenant, fault.EventID, fault.ObservedAt); err != nil || w.State != "pending" {
		t.Fatal(w, err)
	}
	if _, err := s.st.Pool.Exec(ctx, `DROP TRIGGER itest_bind_fault ON ingest_flow_evidence_candidate`); err != nil {
		t.Fatal(err)
	}
	if fresh, err := j.BindFlowEvidence(ctx, fault, fb, fh); err != nil || !fresh {
		t.Fatal("healthy after failure", fresh, err)
	}
	if _, err := s.st.Pool.Exec(ctx, `UPDATE ingest_flow_evidence_candidate SET evidence='{}'::bytea WHERE event_id=$1`, fault.EventID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := j.ReadFlowEvidence(ctx, tenant, fault.EventID, fault.ObservedAt); !errors.Is(err, ingestjournal.ErrSnapshot) {
		t.Fatal("corruption read", err)
	}
	if _, err := j.BindFlowEvidence(ctx, fault, fb, fh); !errors.Is(err, ingestjournal.ErrSnapshot) {
		t.Fatal("corruption repaired silently", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := j.BindFlowEvidence(cancelled, e, b, h); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation", err)
	}
	var runs int
	if err := s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM flow_runs WHERE flow_id=$1`, id).Scan(&runs); err != nil || runs != 0 {
		t.Fatal("executed flow", runs, err)
	}
}
