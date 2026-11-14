package ingestprocessing

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Record is retained evidence, not permission to execute. Planning quarantine
// is Plan.Blocked, distinct from an uncertain already-started attempt. A future
// operator view must show both; it must never label pending/unknown as delivered.
type Record struct {
	State     string `json:"state"`
	AttemptID string `json:"attempt_id,omitempty"`
	Plan      Plan   `json:"plan"`
}

func validState(state, attempt string) bool {
	if state == "pending" {
		return attempt == ""
	}
	if state != "executing" && state != "completed" && state != "outcome_unknown" {
		return false
	}
	_, err := uuid.Parse(attempt)
	return err == nil
}

// Read is tenant-scoped and works while processing is disabled, for inspection.
// No new transaction, claim, lease, effect or automatic repair is implied.
func (x Engine) Read(ctx context.Context, tenant, event string, at time.Time) (Record, error) {
	var envelope, raw, digest []byte
	var rec Record
	var storedPolicy string
	at = at.UTC().Truncate(time.Microsecond)
	err := x.Pool.QueryRow(ctx, `SELECT envelope,plan,plan_sha256,state,COALESCE(attempt_id::text,''),policy FROM ingest_processing_v1 WHERE tenant_id=$1 AND event_id=$2 AND observed_at=$3`, tenant, event, at).Scan(&envelope, &raw, &digest, &rec.State, &rec.AttemptID, &storedPolicy)
	if err != nil {
		return Record{}, err
	}
	var e Envelope
	if json.Unmarshal(envelope, &e) != nil {
		return Record{}, ErrInput
	}
	normalized, b, err := canonical(e)
	if err != nil || !bytes.Equal(b, envelope) || normalized.TenantID != tenant || normalized.EventID != event || !normalized.ObservedAt.Equal(at) || storedPolicy != policy || !validState(rec.State, rec.AttemptID) {
		return Record{}, ErrPlan
	}
	rec.Plan, err = decodePlan(raw, digest, normalized)
	if err != nil {
		return Record{}, err
	}
	return rec, nil
}
