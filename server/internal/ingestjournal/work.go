package ingestjournal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrState = errors.New("admission work state or attempt does not match")

// Work is an UNWIRED state-model candidate, not permission to run any stage.
// Completed records an explicit caller checkpoint, NOT delivery or actuation.
// There is deliberately no expiry, automatic requeue, takeover or delete API.
// Until immutable config/effect bindings and effect-specific acceptance exist,
// no product worker may use these methods to run or retry effects.
type Work struct {
	Envelope  Envelope
	State     string
	AttemptID string
}

func decodeWork(b []byte, tenant, event string, at time.Time, state, attempt string) (Work, error) {
	var e Envelope
	if json.Unmarshal(b, &e) != nil {
		return Work{}, ErrEnvelope
	}
	canonicalEvent, canonicalBytes, err := canonical(e)
	if err != nil || !bytes.Equal(b, canonicalBytes) || canonicalEvent.TenantID != tenant || canonicalEvent.EventID != event || !canonicalEvent.ObservedAt.Equal(at.UTC().Truncate(time.Microsecond)) {
		return Work{}, ErrEnvelope
	}
	switch state {
	case "pending":
		if attempt != "" {
			return Work{}, ErrState
		}
	case "executing", "completed", "outcome_unknown":
		if _, err := uuid.Parse(attempt); err != nil {
			return Work{}, ErrState
		}
	default:
		return Work{}, ErrState
	}
	return Work{canonicalEvent, state, attempt}, nil
}

// ReadWork is tenant-scoped evidence only. Executing surviving restart is not
// considered safe to retry, even if a caller thinks its lease would have expired.
func (j Journal) ReadWork(ctx context.Context, tenant, event string, at time.Time) (Work, error) {
	var b []byte
	var state, attempt string
	err := j.Pool.QueryRow(ctx, `SELECT envelope,state,COALESCE(attempt_id::text,'') FROM ingest_admission_candidate WHERE tenant_id=$1 AND event_id=$2 AND observed_at=$3`, tenant, event, at.UTC().Truncate(time.Microsecond)).Scan(&b, &state, &attempt)
	if err != nil {
		return Work{}, err
	}
	return decodeWork(b, tenant, event, at, state, attempt)
}

// Claim records at most ONE attempt for this immutable admission. It has no
// deadline or lease clock and is not an executor. A commit error is uncertain:
// inspect ReadWork; never infer permission to perform an effect from that error.
func (j Journal) Claim(ctx context.Context, tenant, event string, at time.Time) (Work, error) {
	tx, err := j.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Work{}, err
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = tx.Rollback(c)
	}()
	var b []byte
	var state, attempt string
	at = at.UTC().Truncate(time.Microsecond)
	err = tx.QueryRow(ctx, `SELECT envelope,state,COALESCE(attempt_id::text,'') FROM ingest_admission_candidate WHERE tenant_id=$1 AND event_id=$2 AND observed_at=$3 FOR UPDATE`, tenant, event, at).Scan(&b, &state, &attempt)
	if err != nil {
		return Work{}, err
	}
	w, err := decodeWork(b, tenant, event, at, state, attempt)
	if err != nil {
		return Work{}, err
	}
	if w.State != "pending" {
		return Work{}, ErrState
	}
	w.State = "executing"
	w.AttemptID = uuid.NewString()
	if _, err = tx.Exec(ctx, `UPDATE ingest_admission_candidate SET state='executing',attempt_id=$4 WHERE tenant_id=$1 AND event_id=$2 AND observed_at=$3`, tenant, event, at, w.AttemptID); err != nil {
		return Work{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Work{}, fmt.Errorf("claim commit outcome requires reconciliation: %w", err)
	}
	return w, nil
}

// Complete records only an internal checkpoint from the matching attempt. It
// cannot settle an already-quarantined attempt, and never asserts effect receipt.
func (j Journal) Complete(ctx context.Context, tenant, event string, at time.Time, attempt string) error {
	return j.closeAttempt(ctx, tenant, event, at, attempt, "completed")
}

// MarkUnknown quarantines an interrupted/uncertain matching attempt. Unknown is
// terminal here. This is not a retry button and cannot rearm completed work.
func (j Journal) MarkUnknown(ctx context.Context, tenant, event string, at time.Time, attempt string) error {
	return j.closeAttempt(ctx, tenant, event, at, attempt, "outcome_unknown")
}

func (j Journal) closeAttempt(ctx context.Context, tenant, event string, at time.Time, attempt, state string) error {
	if _, err := uuid.Parse(attempt); err != nil {
		return ErrState
	}
	tx, err := j.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = tx.Rollback(c)
	}()
	var b []byte
	var current, original string
	at = at.UTC().Truncate(time.Microsecond)
	err = tx.QueryRow(ctx, `SELECT envelope,state,COALESCE(attempt_id::text,'') FROM ingest_admission_candidate WHERE tenant_id=$1 AND event_id=$2 AND observed_at=$3 FOR UPDATE`, tenant, event, at).Scan(&b, &current, &original)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrState
	}
	if err != nil {
		return err
	}
	if _, err = decodeWork(b, tenant, event, at, current, original); err != nil {
		return err
	}
	if current != "executing" || original != attempt {
		return ErrState
	}
	if _, err = tx.Exec(ctx, `UPDATE ingest_admission_candidate SET state=$4 WHERE tenant_id=$1 AND event_id=$2 AND observed_at=$3`, tenant, event, at, state); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("attempt checkpoint outcome requires reconciliation: %w", err)
	}
	return nil
}
