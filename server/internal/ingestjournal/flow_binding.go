package ingestjournal

// Candidate retention ONLY: no runtime caller, claim requirement or executor.
// A flow snapshot omits live dependencies. Retaining it grants no permission to
// run it, and does not make either ingest durability gap safe.
import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// BindFlowEvidence retains ONE exact snapshot against an already-admitted,
// pending canonical input. A row lock serializes it with candidate Claim.
// No replacement after changed configuration, no new binding after Claim and
// no backfill for legacy telemetry lacking admission evidence are allowed.
// An exact retained replay is evidence only, even after the attempt ended.
// This does NOT atomically capture config at telemetry admission time: the gap
// between Admit and Capture remains explicit. No worker may treat this as that.
func (j Journal) BindFlowEvidence(ctx context.Context, e Envelope, b []byte, digest string) (bool, error) {
	if _, err := InspectFlowSnapshot(b, digest, e); err != nil {
		return false, err
	}
	tx, err := j.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = tx.Rollback(c)
	}()
	e, _, err = canonical(e)
	if err != nil {
		return false, err
	}
	var envelope []byte
	var state, attempt string
	err = tx.QueryRow(ctx, `SELECT envelope,state,COALESCE(attempt_id::text,'') FROM ingest_admission_candidate WHERE tenant_id=$1 AND event_id=$2 AND observed_at=$3 FOR UPDATE`, e.TenantID, e.EventID, e.ObservedAt).Scan(&envelope, &state, &attempt)
	if err != nil {
		return false, err
	}
	w, err := decodeWork(envelope, e.TenantID, e.EventID, e.ObservedAt, state, attempt)
	if err != nil {
		return false, err
	}
	_, incoming, err := canonical(e)
	if err != nil || !bytes.Equal(envelope, incoming) || w.Envelope != e {
		return false, ErrConflict
	}
	var prior []byte
	var priorDigest, tenant string
	err = tx.QueryRow(ctx, `SELECT tenant_id,evidence,evidence_sha256 FROM ingest_flow_evidence_candidate WHERE event_id=$1 AND observed_at=$2`, e.EventID, e.ObservedAt).Scan(&tenant, &prior, &priorDigest)
	if err == nil {
		if tenant != e.TenantID {
			return false, ErrConflict
		}
		if _, err := InspectFlowSnapshot(prior, priorDigest, e); err != nil {
			return false, err
		}
		if priorDigest != digest || !bytes.Equal(prior, b) {
			return false, ErrConflict
		}
		return false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if state != "pending" {
		return false, ErrState
	}
	if _, err = tx.Exec(ctx, `INSERT INTO ingest_flow_evidence_candidate(event_id,observed_at,tenant_id,evidence,evidence_sha256) VALUES($1,$2,$3,$4,$5)`, e.EventID, e.ObservedAt, e.TenantID, b, digest); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("evidence binding commit outcome requires reconciliation: %w", err)
	}
	return true, nil
}

// ReadFlowEvidence validates retained bytes against the canonical admitted row,
// not today's published version. Copies returned to a caller are not mutable
// storage. This is inspection, NOT current authorization or safe replay proof.
func (j Journal) ReadFlowEvidence(ctx context.Context, tenant, event string, at time.Time) ([]byte, string, error) {
	var b, envelope []byte
	var digest, state, attempt string
	err := j.Pool.QueryRow(ctx, `SELECT f.evidence,f.evidence_sha256,a.envelope,a.state,COALESCE(a.attempt_id::text,'') FROM ingest_flow_evidence_candidate f JOIN ingest_admission_candidate a ON a.event_id=f.event_id AND a.observed_at=f.observed_at AND a.tenant_id=f.tenant_id WHERE f.tenant_id=$1 AND f.event_id=$2 AND f.observed_at=$3`, tenant, event, at.UTC().Truncate(time.Microsecond)).Scan(&b, &digest, &envelope, &state, &attempt)
	if err != nil {
		return nil, "", err
	}
	w, err := decodeWork(envelope, tenant, event, at, state, attempt)
	if err != nil {
		return nil, "", err
	}
	if _, err = InspectFlowSnapshot(b, digest, w.Envelope); err != nil {
		return nil, "", err
	}
	return b, digest, nil
}
