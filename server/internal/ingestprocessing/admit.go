// Package ingestprocessing is the default-off processing-v1 product path.
// No startup caller enables it. Admission never calls legacy evaluators or
// sends effects. Unsupported dependencies are retained as planning quarantine,
// never silently omitted or treated as permission to execute.
package ingestprocessing

import (
	"bytes"
	"context"
	"crypto/sha256"
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
const maxPlanBytes = 262144
const policy = "processing-v1"

var (
	ErrDisabled = errors.New("processing-v1 is disabled")
	ErrInput    = errors.New("processing-v1 requires a bounded canonical reading with a stable observation timestamp")
	ErrConflict = errors.New("processing-v1 input identity conflicts with retained evidence")
	ErrLegacy   = errors.New("telemetry exists without processing evidence; historical effects unknown")
	ErrCapacity = errors.New("processing-v1 retained capacity reached")
	ErrPlan     = errors.New("processing-v1 plan invalid or exceeds supported bounds")
)

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
			return e, nil, ErrInput
		}
	}
	if e.ObservedAt.IsZero() || e.ObservedAt.Year() < 1970 || e.ObservedAt.Year() > 9999 || math.IsNaN(e.Value) || math.IsInf(e.Value, 0) || len(e.Unit) > 64 || e.SchemaVersion != 1 {
		return e, nil, ErrInput
	}
	switch e.Quality {
	case "measured", "estimated", "missing", "stale":
	default:
		return e, nil, ErrInput
	}
	e.ObservedAt = e.ObservedAt.UTC().Truncate(time.Microsecond)
	if e.Value == 0 {
		e.Value = 0
	}
	b, err := json.Marshal(e)
	if err != nil || len(b) > 8192 {
		return e, nil, ErrInput
	}
	return e, b, nil
}

// Enabled is explicit caller configuration, not a tenant authorization grant or
// an activation endpoint. Zero-value configuration refuses before touching DB.
// Only isolated acceptance tests currently construct an enabled Engine.
// Schema must be installed via the separate reviewed maintenance operator.
type Engine struct {
	Pool    *pgxpool.Pool
	Enabled bool
	// durableLeaf is private and only attached by the separately opted-in verified
	// mTLS transport. Direct Engine admission is not certificate authorization.
	durableLeaf *[32]byte
}
type Admission struct {
	New   bool
	State string
	Plan  Plan
}

// Admit commits telemetry and a complete bounded configuration selection/plan
// together, using a single serializable snapshot and a conservative global
// admission lock. Concurrent configuration changes serialize before or after
// that snapshot; serialization errors require caller reconciliation, not an
// external-effect retry. No external call occurs in this transaction.
//
// Exact duplicates return the ORIGINAL stored plan in any state, before reading
// today's config. A lost commit result is uncertain: repeat the SAME canonical
// input to reconcile. No rearm, takeover, cleanup or legacy backfill exists.
// The timestamp must already be stable across retries. A clockless transport
// cannot call this API safely by substituting each new receive time.
func (x Engine) Admit(ctx context.Context, e Envelope) (Admission, error) {
	if !x.Enabled {
		return Admission{}, ErrDisabled
	}
	e, b, err := canonical(e)
	if err != nil {
		return Admission{}, err
	}
	tx, err := x.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Admission{}, err
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='5s';SET LOCAL statement_timeout='15s'"); err != nil {
		return Admission{}, err
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(731492068214)"); err != nil {
		return Admission{}, err
	}
	if x.durableLeaf != nil {
		if err = guardDurableLeaf(ctx, tx, *x.durableLeaf); err != nil {
			return Admission{}, err
		}
	}
	var original, raw, digest []byte
	var state, owner, storedPolicy, attempt string
	err = tx.QueryRow(ctx, `SELECT envelope,plan,plan_sha256,state,tenant_id,policy,COALESCE(attempt_id::text,'') FROM ingest_processing_v1 WHERE event_id=$1 AND observed_at=$2`, e.EventID, e.ObservedAt).Scan(&original, &raw, &digest, &state, &owner, &storedPolicy, &attempt)
	if err == nil {
		if owner != e.TenantID || storedPolicy != policy || !bytes.Equal(original, b) {
			return Admission{}, ErrConflict
		}
		p, err := decodePlan(raw, digest, e)
		if err != nil {
			return Admission{}, err
		}
		if !validState(state, attempt) {
			return Admission{}, ErrPlan
		}
		return Admission{State: state, Plan: p}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Admission{}, err
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM ingest_processing_v1`).Scan(&count); err != nil {
		return Admission{}, err
	}
	if count >= Capacity {
		return Admission{}, ErrCapacity
	}
	p, err := capture(ctx, tx, e)
	if err != nil {
		return Admission{}, err
	}
	raw, err = json.Marshal(p)
	if err != nil || len(raw) > maxPlanBytes {
		return Admission{}, ErrPlan
	}
	sum := sha256.Sum256(raw)
	if _, err = decodePlan(raw, sum[:], e); err != nil {
		return Admission{}, err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,value,unit,quality,schema_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(event_id,observed_at) DO NOTHING`, e.EventID, e.TenantID, e.GatewayID, e.DeviceID, e.PointID, e.ObservedAt, e.Value, e.Unit, e.Quality, e.SchemaVersion)
	if err != nil {
		return Admission{}, err
	}
	if tag.RowsAffected() != 1 {
		return Admission{}, ErrLegacy
	}
	if _, err = tx.Exec(ctx, `INSERT INTO ingest_processing_v1(event_id,observed_at,tenant_id,envelope,plan,plan_sha256,policy) VALUES($1,$2,$3,$4,$5,$6,$7)`, e.EventID, e.ObservedAt, e.TenantID, b, raw, sum[:], policy); err != nil {
		return Admission{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Admission{}, fmt.Errorf("processing admission commit uncertain; reconcile identical input: %w", err)
	}
	return Admission{New: true, State: "pending", Plan: p}, nil
}
func rollback(tx pgx.Tx) {
	c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = tx.Rollback(c)
}
