// Package ingestjournal is an UNWIRED admission candidate, not an enabled
// recovery worker. No product startup installs its table or calls Admit.
// It changes neither MQTT acknowledgement nor external-effect behavior.
package ingestjournal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const Capacity = 100000

var (
	ErrConflict = errors.New("admission identity conflict")
	ErrLegacy   = errors.New("telemetry already exists without admission evidence")
	ErrCapacity = errors.New("admission candidate capacity reached")
	ErrEnvelope = errors.New("invalid admission envelope")
)

// Envelope binds the canonical reading and original candidate work intent.
// No config snapshot or effect plan is inferred. Exactly matching duplicates
// preserve the first work record and its state, even if telemetry is later retained away.
type Envelope struct {
	EventID       string    `json:"event_id"`
	TenantID      string    `json:"tenant_id"`
	GatewayID     string    `json:"gateway_id"`
	DeviceID      string    `json:"device_id"`
	PointID       string    `json:"point_id"`
	ObservedAt    time.Time `json:"observed_at"`
	Value         float64   `json:"value"`
	Unit          string    `json:"unit"`
	Quality       string    `json:"quality"`
	SchemaVersion int       `json:"schema_version"`
}

func canonical(e Envelope) (Envelope, []byte, error) {
	for _, s := range []string{e.EventID, e.TenantID, e.GatewayID, e.DeviceID, e.PointID} {
		if strings.TrimSpace(s) == "" || len(s) > 256 {
			return e, nil, ErrEnvelope
		}
	}
	if e.ObservedAt.IsZero() || e.ObservedAt.Year() < 1970 || e.ObservedAt.Year() > 9999 || math.IsNaN(e.Value) || math.IsInf(e.Value, 0) || len(e.Unit) > 64 || e.SchemaVersion != 1 {
		return e, nil, ErrEnvelope
	}
	switch e.Quality {
	case "measured", "estimated", "missing", "stale":
	default:
		return e, nil, ErrEnvelope
	}
	// Match PostgreSQL's microsecond timestamp precision before hashing/comparing.
	e.ObservedAt = e.ObservedAt.UTC().Truncate(time.Microsecond)
	if e.Value == 0 {
		e.Value = 0
	} // normalize IEEE negative zero
	b, err := json.Marshal(e)
	if err != nil || len(b) > 8192 {
		return e, nil, ErrEnvelope
	}
	return e, b, nil
}

type Journal struct{ Pool *pgxpool.Pool }

// Admit returns true only for a newly COMMITTED reading+pending intent pair.
// False,nil means exact journal replay in ANY state, NOT derived work completed or delivered.
// Any commit error is an uncertain result; an exact retry reconciles evidence.
// The existing telemetry global(event_id,observed_at) key is preserved: another
// tenant/payload cannot silently take over a collision. No old telemetry is
// backfilled into pending work, because its past effects are unknown.
func (j Journal) Admit(ctx context.Context, e Envelope) (bool, error) {
	e, b, err := canonical(e)
	if err != nil {
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
	// Candidate-wide serialization gives an exact hard cap under concurrency.
	// This is intentionally conservative, NOT production throughput acceptance.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(731492068213)`); err != nil {
		return false, err
	}
	var original []byte
	err = tx.QueryRow(ctx, `SELECT envelope FROM ingest_admission_candidate WHERE event_id=$1 AND observed_at=$2`, e.EventID, e.ObservedAt).Scan(&original)
	if err == nil {
		if !bytes.Equal(original, b) {
			return false, ErrConflict
		}
		return false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	var n int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM ingest_admission_candidate`).Scan(&n); err != nil {
		return false, err
	}
	if n >= Capacity {
		return false, ErrCapacity
	}
	tag, err := tx.Exec(ctx, `INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,value,unit,quality,schema_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(event_id,observed_at) DO NOTHING`, e.EventID, e.TenantID, e.GatewayID, e.DeviceID, e.PointID, e.ObservedAt, e.Value, e.Unit, e.Quality, e.SchemaVersion)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() != 1 {
		return false, ErrLegacy
	}
	if _, err = tx.Exec(ctx, `INSERT INTO ingest_admission_candidate(event_id,observed_at,tenant_id,envelope) VALUES($1,$2,$3,$4)`, e.EventID, e.ObservedAt, e.TenantID, b); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("admission commit outcome requires reconciliation: %w", err)
	}
	return true, nil
}

// Pending reads only the requested tenant's immutable intent. It is not a
// claim, lease, permission to run or completion assertion. No cleanup exists:
// all records count against Capacity and full storage refuses new admission.
func (j Journal) Pending(ctx context.Context, tenant, event string, at time.Time) (Envelope, error) {
	var b []byte
	var e Envelope
	err := j.Pool.QueryRow(ctx, `SELECT envelope FROM ingest_admission_candidate WHERE tenant_id=$1 AND event_id=$2 AND observed_at=$3 AND state='pending'`, tenant, event, at.UTC().Truncate(time.Microsecond)).Scan(&b)
	if err != nil {
		return e, err
	}
	if err = json.Unmarshal(b, &e); err != nil {
		return Envelope{}, ErrEnvelope
	}
	canonicalEvent, canonicalBytes, err := canonical(e)
	if err != nil || !bytes.Equal(b, canonicalBytes) || canonicalEvent.TenantID != tenant || canonicalEvent.EventID != event || !canonicalEvent.ObservedAt.Equal(at.UTC().Truncate(time.Microsecond)) {
		return Envelope{}, ErrEnvelope
	}
	return canonicalEvent, nil
}
