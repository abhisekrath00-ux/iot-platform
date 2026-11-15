package ingestprocessing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrQuarantined = errors.New("processing plan has unresolved stages; execution quarantined")
	ErrState       = errors.New("processing input is not pending; no takeover or retry allowed")
	ErrRevoked     = errors.New("processing configuration or authorization changed; retained targets will not be substituted")
	ErrUnknown     = errors.New("processing attempt outcome unknown; never automatically retry effects")
)

// Sender performs exactly one bounded external attempt, with no retry or
// redirect. nil means provider acceptance, NEVER final recipient delivery.
// Production SMTP/Slack adapters are deliberately NOT wired here: their timeout
// and application-result handling need separate acceptance. The separate staged
// Slack and SMTP adapters is exercised only against synthetic TLS fixtures, with no
// production startup wiring or real-provider acceptance.
type Sender interface {
	SendOnce(context.Context, Envelope, Intent) error
}

// ExecuteOnce consumes a whole immutable plan at most once. The attempt and all
// supported internal history are committed BEFORE external I/O. Interrupted or
// uncertain batches quarantine ALL their occurrences, including those that may
// never have started. This conservative batch policy trades automatic recovery
// for no duplicate effects; it does NOT claim per-effect receipt precision.
// There are no goroutines, leases, expiry, startup sweeps or automatic retries.
// A process crash leaves executing evidence which is inspectable but unclaimable.
// No production startup caller, transport ACK or scheduler invokes this method.
func (x Engine) ExecuteOnce(ctx context.Context, tenant, event string, at time.Time, s Sender) error {
	if !x.Enabled {
		return ErrDisabled
	}
	rec, err := x.startAttempt(ctx, tenant, event, at, s != nil)
	if err != nil {
		return err
	}
	unknown := func(cause error) error {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		mark := x.finishAttempt(c, rec, "outcome_unknown")
		return fmt.Errorf("%w: attempt %s; %v; quarantine checkpoint: %v", ErrUnknown, rec.AttemptID, cause, mark)
	}
	for _, intent := range rec.Plan.Intents {
		// Delays are relative to durable selection, not reset after a process wait.
		due := rec.Plan.SelectedAt.Add(time.Duration(intent.DelayNS))
		wait := time.Until(due)
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return unknown(ctx.Err())
			case <-timer.C:
			}
		}
		if err = ctx.Err(); err != nil {
			return unknown(err)
		}
		// One fresh consistent read immediately before each attempt. A changed or
		// disabled source/target/policy refuses; never retarget to today's channel.
		if err = x.authorize(ctx, rec.Plan); err != nil {
			return unknown(err)
		}
		sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = s.SendOnce(sendCtx, rec.Plan.Envelope, intent)
		cancel()
		if err != nil {
			return unknown(err)
		}
	}
	if err = x.finishAttempt(ctx, rec, "completed"); err != nil {
		return unknown(err)
	}
	return nil
}
func (x Engine) startAttempt(ctx context.Context, tenant, event string, at time.Time, hasSender bool) (Record, error) {
	tx, err := x.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Record{}, err
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='5s';SET LOCAL statement_timeout='15s'"); err != nil {
		return Record{}, err
	}
	var state string
	if err = tx.QueryRow(ctx, `SELECT state FROM ingest_processing_v1 WHERE tenant_id=$1 AND event_id=$2 AND observed_at=$3 FOR UPDATE`, tenant, event, at.UTC().Truncate(time.Microsecond)).Scan(&state); err != nil {
		return Record{}, err
	}
	rec, err := read(ctx, tx, tenant, event, at)
	if err != nil {
		return Record{}, err
	}
	if rec.State != "pending" {
		return Record{}, ErrState
	}
	if len(rec.Plan.Blocked) > 0 {
		return Record{}, ErrQuarantined
	}
	if len(rec.Plan.Intents) > 0 && !hasSender {
		return Record{}, errors.New("a reviewed one-attempt sender is required")
	}
	if err = authorize(ctx, tx, rec.Plan); err != nil {
		return Record{}, err
	}
	if err = applyPosition(ctx, tx, rec.Plan); err != nil {
		return Record{}, err
	}
	rec.AttemptID = uuid.NewString()
	rec.State = "executing"
	// Deterministic history IDs bind input+flow; unlike the old evaluator, no
	// notification is labelled notified before the external attempt even starts.
	for _, f := range matchingFlows(rec.Plan) {
		_, err = tx.Exec(ctx, `INSERT INTO flow_runs(id,flow_id,trigger_value,outcome,detail) VALUES($1,$2,$3,'processing_started',$4)`, runID(rec.Plan.Envelope, f.ID), f.ID, rec.Plan.Envelope.Value, "processing-v1 attempt "+rec.AttemptID+"; provider delivery not asserted")
		if err != nil {
			return Record{}, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE ingest_processing_v1 SET state='executing',attempt_id=$4 WHERE tenant_id=$1 AND event_id=$2 AND observed_at=$3`, tenant, event, rec.Plan.Envelope.ObservedAt, rec.AttemptID); err != nil {
		return Record{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Record{}, fmt.Errorf("claim commit uncertain; inspect without sending: %w", err)
	}
	return rec, nil
}
func matchingFlows(p Plan) []Flow {
	var out []Flow
	for _, f := range p.Flows {
		var d flow.Definition
		if json.Unmarshal(f.Definition, &d) != nil {
			continue
		}
		trig := d.Trig()
		if !d.IsScheduled() && trig.DeviceID == p.Envelope.DeviceID && trig.PointID == p.Envelope.PointID {
			out = append(out, f)
		}
	}
	return out
}
func runID(e Envelope, flowID string) string {
	_, b, _ := canonical(e)
	sum := sha256.Sum256(append(append(b, 0), []byte(flowID)...))
	return "processing-" + hex.EncodeToString(sum[:])
}
func (x Engine) authorize(ctx context.Context, p Plan) error {
	tx, err := x.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "SET LOCAL statement_timeout='15s'"); err != nil {
		return err
	}
	return authorize(ctx, tx, p)
}
func authorize(ctx context.Context, tx pgx.Tx, p Plan) error {
	current, err := capture(ctx, tx, p.Envelope)
	if err != nil {
		return fmt.Errorf("%w: current policy unreadable", ErrRevoked)
	}
	current.SelectedAt = p.SelectedAt
	a, err := json.Marshal(current)
	if err != nil {
		return ErrRevoked
	}
	b, err := json.Marshal(p)
	if err != nil || !bytes.Equal(a, b) {
		return ErrRevoked
	}
	return nil
}
func (x Engine) finishAttempt(ctx context.Context, rec Record, state string) error {
	if state != "completed" && state != "outcome_unknown" {
		return ErrState
	}
	tx, err := x.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='5s';SET LOCAL statement_timeout='15s'"); err != nil {
		return err
	}
	e := rec.Plan.Envelope
	tag, err := tx.Exec(ctx, `UPDATE ingest_processing_v1 SET state=$5 WHERE tenant_id=$1 AND event_id=$2 AND observed_at=$3 AND attempt_id=$4 AND state='executing'`, e.TenantID, e.EventID, e.ObservedAt, rec.AttemptID, state)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrState
	}
	for _, f := range matchingFlows(rec.Plan) {
		tag, err = tx.Exec(ctx, `UPDATE flow_runs SET outcome=$3 WHERE id=$1 AND flow_id=$2 AND outcome='processing_started'`, runID(e, f.ID), f.ID, "processing_"+state)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 && state == "completed" {
			return ErrState
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("completion commit uncertain; inspect retained state: %w", err)
	}
	return nil
}
