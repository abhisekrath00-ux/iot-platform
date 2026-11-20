package authorityschema

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// IdentityEvidence fingerprints ONLY the fixed selected exact95 authority tables
// below. It is not a signature, secret backup, full database/image restore proof,
// owner permission, key custody or runtime acceptance. Preserve it privately and
// independently BEFORE backup with all writers stopped, with trusted provenance.
// A witness taken from the same backup being checked cannot prove freshness.
// Even a successful check says nothing about later writes or a hostile DB/server.
// No startup, transport or automatic recovery consumes this maintenance result.
type IdentityEvidence struct {
	Version           int    `json:"version"`
	Status            string `json:"status"`
	Tables            int    `json:"tables"`
	Rows              int64  `json:"rows"`
	SHA256            string `json:"sha256"`
	RuntimeAccepted   bool   `json:"runtime_accepted"`
	ProcessingEnabled bool   `json:"processing_enabled"`
}

// Fixed identifiers, never caller-controlled SQL. Every column of every row in
// each table participates, including stored password verifiers/session metadata.
// Only a digest/count is returned, no row values. Treat even the witness as private:
// it is an equality oracle for low-entropy changes, not anonymized public data.
var identityTables = [...]string{
	"tenants", "customers", "sites", "users", "user_sessions", "user_customer_scope",
	"gateways", "edge_identity_migrations", "edge_pairing_proofs", "audit_log",
	"admission_denial_epoch_v1", "admission_denied_leaf_v1",
}

const identityRowLimit = 100000
const identitySingleRowByteLimit = 1 << 20
const identityByteLimit = 64 << 20

// InspectIdentityEvidence is a bounded read-only LOCAL MAINTENANCE operation.
// SHARE NOWAIT fences inserts/updates/deletes on ALL selected tables, including
// absent scope rows, before any data snapshot is read. Busy writers refuse; no
// waiting behind them, no repair, no effect retry. These table locks temporarily
// block ALL selected-table writers, not just one tenant, and are NOT for runtime.
// UTC/ISO/UTF8 and sorted JSONB text make logical dumps with the same PostgreSQL
// JSONB formatting comparable. Cross-major PostgreSQL formatting is unproven.
// Per-table100000 rows, per-row1MiB and total64MiB of canonical data cap output processing;
// SQL sorting/parsing still consumes DB resources, bounded by the15s context.
func InspectIdentityEvidence(ctx context.Context, p *pgxpool.Pool, dir string) (IdentityEvidence, error) {
	if ctx == nil || p == nil {
		return IdentityEvidence{}, ErrSchema
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if e := checkBytes(); e != nil {
		return IdentityEvidence{}, e
	}
	tx, e := begin(ctx, p)
	if e != nil {
		return IdentityEvidence{}, ErrSchema
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(ctx, "SET LOCAL TIME ZONE 'UTC'; SET LOCAL DateStyle='ISO, YMD'; SET LOCAL IntervalStyle='postgres'; SET LOCAL extra_float_digits=3; SET LOCAL client_encoding='UTF8'; SET LOCAL search_path=public,pg_catalog; SET LOCAL statement_timeout='10s'"); e != nil {
		return IdentityEvidence{}, ErrSchema
	}
	// Fixed list must stay in the same order as the fingerprint. The strong table
	// fence is also required for audit history and absent identity/scope records.
	if _, e = tx.Exec(ctx, "LOCK TABLE public.tenants,public.customers,public.sites,public.users,public.user_sessions,public.user_customer_scope,public.gateways,public.edge_identity_migrations,public.edge_pairing_proofs,public.audit_log,public.admission_denial_epoch_v1,public.admission_denied_leaf_v1 IN SHARE MODE NOWAIT"); e != nil {
		return IdentityEvidence{}, ErrSchema
	}
	if e = inspect(ctx, tx, dir); e != nil {
		return IdentityEvidence{}, ErrSchema
	}
	h := sha256.New()
	h.Write([]byte("HexThings/selected-identity95/logical-evidence/v1\x00"))
	var frame [8]byte
	appendFrame := func(b []byte) { binary.BigEndian.PutUint64(frame[:], uint64(len(b))); h.Write(frame[:]); h.Write(b) }
	var total, used int64
	// Include the selected catalog projection too, even for EMPTY tables. Core
	// exact95 inspection is strict only for its own catalog, not every table here.
	// This detects baseline-relative structural drift, not arbitrary role/grant,
	// function body, sequence, database setting or hostile-catalog changes.
	catalog, err := tx.Query(ctx, shapeSQL, identityTables[:])
	if err != nil {
		return IdentityEvidence{}, ErrSchema
	}
	for catalog.Next() {
		var kind, relation, name, definition string
		if e = catalog.Scan(&kind, &relation, &name, &definition); e != nil {
			catalog.Close()
			return IdentityEvidence{}, ErrSchema
		}
		data, err := json.Marshal([]string{kind, relation, name, definition})
		if err != nil {
			catalog.Close()
			return IdentityEvidence{}, ErrSchema
		}
		used += int64(len(data))
		if used > identityByteLimit {
			catalog.Close()
			return IdentityEvidence{}, ErrSchema
		}
		appendFrame(data)
	}
	e = catalog.Err()
	catalog.Close()
	if e != nil {
		return IdentityEvidence{}, ErrSchema
	}
	appendFrame([]byte("selected-rows"))
	for _, table := range identityTables {
		appendFrame([]byte(table))
		// JSONB object keys have deterministic ordering. C collation also fixes row
		// order independently of the database locale. Duplicate rows still count.
		rows, err := tx.Query(ctx, "SELECT CASE WHEN octet_length(data)<=$1 THEN data ELSE NULL END FROM (SELECT to_jsonb(t)::text AS data FROM public."+table+" t ORDER BY to_jsonb(t)::text COLLATE \"C\" LIMIT 100001) bounded", identitySingleRowByteLimit)
		if err != nil {
			return IdentityEvidence{}, ErrSchema
		}
		n := int64(0)
		for rows.Next() {
			var data string
			if e = rows.Scan(&data); e != nil {
				rows.Close()
				return IdentityEvidence{}, ErrSchema
			}
			n++
			used += int64(len(data))
			if n > identityRowLimit || used > identityByteLimit {
				rows.Close()
				return IdentityEvidence{}, ErrSchema
			}
			appendFrame([]byte(data))
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return IdentityEvidence{}, ErrSchema
		}
		// Length-framed rows plus a count end each fixed table without ambiguous
		// concatenation or relying only on global row counts.
		binary.BigEndian.PutUint64(frame[:], uint64(n))
		h.Write(frame[:])
		total += n
	}
	if e = ctx.Err(); e != nil {
		return IdentityEvidence{}, ErrSchema
	}
	return IdentityEvidence{Version: 1, Status: "exact_staged_selected_identity_95_v1", Tables: len(identityTables), Rows: total, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

// VerifyIdentityEvidence compares selected-table continuity to an independently
// preserved pre-backup witness. It never repairs, restores or authorizes startup.
func VerifyIdentityEvidence(ctx context.Context, p *pgxpool.Pool, dir string, want IdentityEvidence) error {
	b, e := hex.DecodeString(want.SHA256)
	if e != nil || len(b) != 32 || want.SHA256 != hex.EncodeToString(b) || want.Version != 1 || want.Status != "exact_staged_selected_identity_95_v1" || want.Tables != len(identityTables) || want.Rows < 0 || want.Rows > int64(len(identityTables)*identityRowLimit) || want.RuntimeAccepted || want.ProcessingEnabled {
		return ErrSchema
	}
	got, e := InspectIdentityEvidence(ctx, p, dir)
	if e != nil {
		return e
	}
	if got != want {
		return errors.New("selected identity authority continuity differs; do not activate or repair from this backup")
	}
	return nil
}
