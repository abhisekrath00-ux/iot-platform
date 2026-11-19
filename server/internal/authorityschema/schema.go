// Package authorityschema is the separate, default-off denial95 maintenance
// path. It never runs at startup and never authenticates or authorizes an owner.
package authorityschema

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/processingschema"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const Filename = "0095_admission_denial_v1.sql"
const migrationLock int64 = 0x4845584D4F4E

var ErrSchema = errors.New("exact staged denial95 authority unavailable; no repair or activation permitted")

//go:embed 0095_admission_denial_v1.sql
var stagedSQL string

//go:embed catalog.json
var catalogJSON []byte

//go:embed schema-shape.sql
var shapeSQL string

//go:embed shape.json
var expectedShape []byte

func checkBytes() error {
	var c map[string]string
	sum := sha256.Sum256([]byte(stagedSQL))
	if json.Unmarshal(catalogJSON, &c) != nil || len(c) != 1 || c[Filename] != hex.EncodeToString(sum[:]) {
		return ErrSchema
	}
	return nil
}
func begin(ctx context.Context, p *pgxpool.Pool) (pgx.Tx, error) {
	if ctx == nil || p == nil {
		return nil, ErrSchema
	}
	tx, e := p.Begin(ctx)
	if e != nil {
		return nil, e
	}
	if _, e = tx.Exec(ctx, "SET LOCAL lock_timeout='5s';SET LOCAL statement_timeout='30s'"); e == nil {
		_, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLock)
	}
	if e != nil {
		_ = tx.Rollback(context.Background())
		return nil, e
	}
	return tx, nil
}

// Inspect accepts exactly the separate95 ledger and all core93/94/95 shapes.
// Exact93 and exact94 public inspectors still REFUSE this database. No repair.
func Inspect(ctx context.Context, p *pgxpool.Pool, dir string) error {
	if e := checkBytes(); e != nil {
		return e
	}
	tx, e := begin(ctx, p)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	return inspect(ctx, tx, dir)
}

// Apply requires an already exact94 database and a separately authorized stopped
// maintenance target/backup/compatibility decision. Other clients are refused,
// not fenced forever. No startup calls this. A commit error is UNCERTAIN: inspect
// before any new decision. Never blindly retry, downgrade, delete or repair.
func Apply(ctx context.Context, p *pgxpool.Pool, dir string) error {
	if ctx == nil || p == nil {
		return ErrSchema
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := checkBytes(); e != nil {
		return e
	}
	if _, e := processingschema.Inspect(ctx, p, dir); e != nil {
		return e
	}
	tx, e := begin(ctx, p)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	var clients int
	if e = tx.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND backend_type='client backend'").Scan(&clients); e != nil || clients != 0 {
		return ErrSchema
	}
	// Validate unchanged94 again inside this SAME transaction before changing it.
	// The expected95 ledger is inserted transactionally only for strict inspection.
	if _, e = tx.Exec(ctx, "INSERT INTO schema_migrations(filename) VALUES($1)", Filename); e != nil {
		return ErrSchema
	}
	if e = processingschema.InspectAuthorityBase(ctx, tx, dir); e != nil {
		return e
	}
	for _, n := range []string{"admission_denial_epoch_v1", "admission_denied_leaf_v1"} {
		var v *string
		if e = tx.QueryRow(ctx, "SELECT to_regclass($1)::text", n).Scan(&v); e != nil || v != nil {
			return ErrSchema
		}
	}
	if _, e = tx.Exec(ctx, stagedSQL); e != nil {
		return ErrSchema
	}
	if e = inspect(ctx, tx, dir); e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return errors.New("denial95 schema commit uncertain; inspect exact ledger and shapes before deciding")
	}
	return nil
}
func inspect(ctx context.Context, tx pgx.Tx, dir string) error {
	if e := processingschema.InspectAuthorityBase(ctx, tx, dir); e != nil {
		return e
	}
	got, e := shape(ctx, tx)
	if e != nil {
		return e
	}
	var want []any
	if json.Unmarshal(expectedShape, &want) != nil || !reflect.DeepEqual(got, want) {
		return ErrSchema
	}
	var n int
	var rev int64
	if e = tx.QueryRow(ctx, "SELECT count(*),COALESCE(min(revision),-1) FROM admission_denial_epoch_v1 WHERE singleton=true").Scan(&n, &rev); e != nil || n != 1 || rev < 0 {
		return ErrSchema
	}
	return nil
}

// shape is the read-only catalog projection used by exact inspection.
func shape(ctx context.Context, tx pgx.Tx) ([]any, error) {
	var schema string
	if e := tx.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); e != nil {
		return nil, e
	}
	rows, e := tx.Query(ctx, shapeSQL, []string{"admission_denial_epoch_v1", "admission_denied_leaf_v1"})
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	got := []any{}
	for rows.Next() {
		var kind, relation, name, raw string
		if e = rows.Scan(&kind, &relation, &name, &raw); e != nil {
			return nil, e
		}
		var d map[string]any
		if json.Unmarshal([]byte(raw), &d) != nil {
			return nil, ErrSchema
		}
		if kind == "index" {
			text, _ := d["definition"].(string)
			for _, ns := range []string{schema, pgx.Identifier{schema}.Sanitize()} {
				for _, table := range []string{relation, pgx.Identifier{relation}.Sanitize()} {
					text = strings.Replace(text, " ON "+ns+"."+table+" ", " ON "+relation+" ", 1)
				}
			}
			d["definition"] = text
		}
		got = append(got, map[string]any{"kind": kind, "relation": relation, "name": name, "definition": d})
	}
	return got, rows.Err()
}
