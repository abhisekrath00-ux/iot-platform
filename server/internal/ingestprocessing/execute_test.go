package ingestprocessing

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type syntheticSender func(context.Context, Envelope, Intent) error

func (s syntheticSender) SendOnce(ctx context.Context, e Envelope, i Intent) error {
	return s(ctx, e, i)
}
func mustAdmit(t *testing.T, x Engine, e Envelope) {
	t.Helper()
	if _, err := x.Admit(t.Context(), e); err != nil {
		t.Fatal(err)
	}
}
func assertState(t *testing.T, x Engine, e Envelope, want string) {
	t.Helper()
	r, err := x.Read(t.Context(), e.TenantID, e.EventID, e.ObservedAt)
	if err != nil || r.State != want {
		t.Fatal(r, err)
	}
}
func historyCount(t *testing.T, x Engine, want int, outcome string) {
	t.Helper()
	var n int
	if err := x.Pool.QueryRow(t.Context(), `SELECT count(*) FROM flow_runs WHERE outcome=$1`, outcome).Scan(&n); err != nil || n != want {
		t.Fatal(n, want, err)
	}
}
func TestExecuteOnceDurableBeforeIOAndNoRearm(t *testing.T) {
	x, e := fixture(t)
	ctx := t.Context()
	mustAdmit(t, x, e)
	var calls int
	s := syntheticSender(func(ctx context.Context, got Envelope, i Intent) error {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("unbounded sender context")
		}
		r, err := x.Read(ctx, e.TenantID, e.EventID, e.ObservedAt)
		if err != nil || r.State != "executing" || r.AttemptID == "" {
			t.Fatal(r, err)
		}
		historyCount(t, x, 1, "processing_started")
		if got != e || i.Sequence != calls || i.Target != "synthetic@example.invalid" {
			t.Fatal(got, i)
		}
		calls++
		return nil
	})
	if err := x.ExecuteOnce(ctx, e.TenantID, e.EventID, e.ObservedAt, s); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	assertState(t, x, e, "completed")
	historyCount(t, x, 1, "processing_completed")
	historyCount(t, x, 0, "notified")
	if err := x.ExecuteOnce(ctx, e.TenantID, e.EventID, e.ObservedAt, s); !errors.Is(err, ErrState) {
		t.Fatal(err)
	}
	if a, err := x.Admit(ctx, e); err != nil || a.New || a.State != "completed" {
		t.Fatal(a, err)
	}
	if calls != 2 {
		t.Fatal("rearmed", calls)
	}
}
func TestExecuteFailureUnknownDoesNotTryRemainingOrRetry(t *testing.T) {
	x, e := fixture(t)
	mustAdmit(t, x, e)
	calls := 0
	s := syntheticSender(func(context.Context, Envelope, Intent) error {
		calls++
		return errors.New("synthetic provider result uncertain after submission")
	})
	if err := x.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, s); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	assertState(t, x, e, "outcome_unknown")
	historyCount(t, x, 1, "processing_outcome_unknown")
	if err := x.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, s); !errors.Is(err, ErrState) {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}
func TestExecuteRefusesChangedPolicyBeforeAndBetweenAttempts(t *testing.T) {
	for _, mode := range []string{"before-target", "before-flow", "between-channel", "between-feature", "between-delete-flow"} {
		t.Run(mode, func(t *testing.T) {
			x, e := fixture(t)
			mustAdmit(t, x, e)
			calls := 0
			if mode == "before-target" {
				exec(t, x.Pool, `UPDATE notification_channels SET target='changed@example.invalid' WHERE id='c'`)
			}
			if mode == "before-flow" {
				exec(t, x.Pool, `UPDATE flows SET enabled=false WHERE id='f'`)
			}
			s := syntheticSender(func(context.Context, Envelope, Intent) error {
				calls++
				switch mode {
				case "between-channel":
					exec(t, x.Pool, `UPDATE notification_channels SET enabled=false WHERE id='c'`)
				case "between-feature":
					exec(t, x.Pool, `INSERT INTO tenant_features(tenant_id,feature,enabled) VALUES('a','http_nodes',true)`)
				case "between-delete-flow":
					exec(t, x.Pool, `DELETE FROM flows WHERE id='f'`)
				}
				return nil
			})
			err := x.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, s)
			if mode == "before-target" || mode == "before-flow" {
				if !errors.Is(err, ErrRevoked) || calls != 0 {
					t.Fatal(calls, err)
				}
				assertState(t, x, e, "pending")
				historyCount(t, x, 0, "processing_started")
			} else {
				if !errors.Is(err, ErrUnknown) || calls != 1 {
					t.Fatal(calls, err)
				}
				assertState(t, x, e, "outcome_unknown")
			}
		})
	}
}
func TestExecuteInternalFaultRollsBackBeforeAnyIO(t *testing.T) {
	x, e := fixture(t)
	mustAdmit(t, x, e)
	calls := 0
	exec(t, x.Pool, `CREATE FUNCTION reject_started() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic history fault';END$$;CREATE TRIGGER reject_started BEFORE INSERT ON flow_runs FOR EACH ROW EXECUTE FUNCTION reject_started()`)
	s := syntheticSender(func(context.Context, Envelope, Intent) error { calls++; return nil })
	if err := x.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, s); err == nil {
		t.Fatal("history fault accepted")
	}
	assertState(t, x, e, "pending")
	historyCount(t, x, 0, "processing_started")
	if calls != 0 {
		t.Fatal(calls)
	}
	exec(t, x.Pool, `DROP TRIGGER reject_started ON flow_runs;DROP FUNCTION reject_started()`)
	if err := x.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, s); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}
func TestExecuteConcurrentClaimOnlyOneBatch(t *testing.T) {
	x, e := fixture(t)
	mustAdmit(t, x, e)
	var calls atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	s := syntheticSender(func(context.Context, Envelope, Intent) error { calls.Add(1); return nil })
	for range 10 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- x.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, s) }()
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
			continue
		}
		if errors.Is(err, ErrState) {
			continue
		}
		var pe interface{ SQLState() string }
		if !errors.As(err, &pe) || pe.SQLState() != "40001" {
			t.Fatal(err)
		}
	}
	if successes != 1 || calls.Load() != 2 {
		t.Fatal(successes, calls.Load())
	}
	assertState(t, x, e, "completed")
	historyCount(t, x, 1, "processing_completed")
}
func TestExecuteQuarantineDisabledAndMissingSender(t *testing.T) {
	x, e := fixture(t)
	ctx := t.Context()
	mustAdmit(t, x, e)
	if err := x.ExecuteOnce(ctx, e.TenantID, e.EventID, e.ObservedAt, nil); err == nil {
		t.Fatal("missing sender accepted")
	}
	assertState(t, x, e, "pending")
	x.Enabled = false
	if err := x.ExecuteOnce(ctx, e.TenantID, e.EventID, e.ObservedAt, nil); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	x.Enabled = true
	e.EventID = "blocked"
	exec(t, x.Pool, `UPDATE devices SET lat_point='p' WHERE id='d'`)
	mustAdmit(t, x, e)
	if err := x.ExecuteOnce(ctx, e.TenantID, e.EventID, e.ObservedAt, syntheticSender(func(context.Context, Envelope, Intent) error { return fmt.Errorf("must never be called") })); !errors.Is(err, ErrQuarantined) {
		t.Fatal(err)
	}
	assertState(t, x, e, "pending")
	historyCount(t, x, 0, "processing_started")
}
func TestInterruptedStartedBatchUnclaimableAndCancellation(t *testing.T) {
	x, e := fixture(t)
	ctx := t.Context()
	mustAdmit(t, x, e)
	// Simulate process loss immediately after committed start, before any send.
	// Inspection preserves executing/attempt evidence but cannot take it over.
	rec, err := x.startAttempt(ctx, e.TenantID, e.EventID, e.ObservedAt, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = x.ExecuteOnce(ctx, e.TenantID, e.EventID, e.ObservedAt, nil); !errors.Is(err, ErrState) {
		t.Fatal(err)
	}
	r, err := x.Read(ctx, e.TenantID, e.EventID, e.ObservedAt)
	if err != nil || r.State != "executing" || r.AttemptID != rec.AttemptID {
		t.Fatal(r, err)
	}
	// A caller that still owns its attempt can record uncertainty, never rearm it.
	if err = x.finishAttempt(ctx, rec, "outcome_unknown"); err != nil {
		t.Fatal(err)
	}
	e.EventID = "cancel-after-first"
	mustAdmit(t, x, e)
	c, cancel := context.WithCancel(ctx)
	defer cancel()
	calls := 0
	s := syntheticSender(func(context.Context, Envelope, Intent) error { calls++; cancel(); return nil })
	if err = x.ExecuteOnce(c, e.TenantID, e.EventID, e.ObservedAt, s); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	assertState(t, x, e, "outcome_unknown")
}
func TestEmptyAndElapsedDelayNoExternalSuccessClaim(t *testing.T) {
	x, e := fixture(t)
	ctx := t.Context()
	e.Value = 0
	mustAdmit(t, x, e)
	if err := x.ExecuteOnce(ctx, e.TenantID, e.EventID, e.ObservedAt, nil); err != nil {
		t.Fatal(err)
	}
	assertState(t, x, e, "completed")
	historyCount(t, x, 0, "notified")
	// Delay remains tied to selection. Do not reset it when an attempt starts.
	e.EventID = "delay-cancel"
	e.Value = 42
	exec(t, x.Pool, `UPDATE flow_versions SET definition=jsonb_set(definition,'{steps}','[{"type":"delay","seconds":60},{"type":"notify","channel_id":"c","message":"synthetic"}]') WHERE id='v'`)
	mustAdmit(t, x, e)
	c, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	calls := 0
	err := x.ExecuteOnce(c, e.TenantID, e.EventID, e.ObservedAt, syntheticSender(func(context.Context, Envelope, Intent) error { calls++; return nil }))
	if !errors.Is(err, ErrUnknown) || calls != 0 {
		t.Fatal(calls, err)
	}
	assertState(t, x, e, "outcome_unknown")
}
func TestFinishFaultAfterAcceptanceQuarantinesWithoutResend(t *testing.T) {
	x, e := fixture(t)
	mustAdmit(t, x, e)
	exec(t, x.Pool, `CREATE FUNCTION reject_completed_history() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.outcome='processing_completed' THEN RAISE EXCEPTION 'synthetic completion fault';END IF;RETURN NEW;END$$;CREATE TRIGGER reject_completed_history BEFORE UPDATE ON flow_runs FOR EACH ROW EXECUTE FUNCTION reject_completed_history()`)
	calls := 0
	s := syntheticSender(func(context.Context, Envelope, Intent) error { calls++; return nil })
	if err := x.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, s); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	assertState(t, x, e, "outcome_unknown")
	historyCount(t, x, 1, "processing_outcome_unknown")
	historyCount(t, x, 0, "processing_completed")
	if calls != 2 {
		t.Fatal(calls)
	}
	exec(t, x.Pool, `DROP TRIGGER reject_completed_history ON flow_runs;DROP FUNCTION reject_completed_history()`)
	if err := x.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, s); !errors.Is(err, ErrState) {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("resend after completion fault", calls)
	}
}
func TestAttemptStateUpdateFaultRollsBackHistory(t *testing.T) {
	x, e := fixture(t)
	mustAdmit(t, x, e)
	exec(t, x.Pool, `CREATE FUNCTION reject_attempt_start() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic attempt state fault';END$$;CREATE TRIGGER reject_attempt_start BEFORE UPDATE ON ingest_processing_v1 FOR EACH ROW EXECUTE FUNCTION reject_attempt_start()`)
	calls := 0
	s := syntheticSender(func(context.Context, Envelope, Intent) error { calls++; return nil })
	if err := x.ExecuteOnce(t.Context(), e.TenantID, e.EventID, e.ObservedAt, s); err == nil {
		t.Fatal("attempt update fault accepted")
	}
	assertState(t, x, e, "pending")
	historyCount(t, x, 0, "processing_started")
	if calls != 0 {
		t.Fatal(calls)
	}
}
