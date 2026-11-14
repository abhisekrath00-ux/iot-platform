// Package processingschema is the separately staged processing-v1 schema path.
// It never runs on API/ingest startup and does not activate a worker or effects.
package processingschema

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/setupbootstrap"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const Filename = "0094_ingest_processing_v1.sql"
const migrationLock int64 = 0x4845584D4F4E

//go:embed 0094_ingest_processing_v1.sql
var stagedSQL string

//go:embed catalog.json
var catalogJSON []byte

//go:embed schema-shape.sql
var shapeSQL string

//go:embed shape.json
var expectedShapeJSON []byte

type Inspection struct {
	Version           int    `json:"version"`
	Status            string `json:"status"`
	RuntimeAccepted   bool   `json:"runtime_accepted"`
	ProcessingEnabled bool   `json:"processing_enabled"`
}

func checkBytes() error {
	var catalog map[string]string
	if json.Unmarshal(catalogJSON, &catalog) != nil || len(catalog) != 1 {
		return fmt.Errorf("invalid staged processing catalog")
	}
	sum := sha256.Sum256([]byte(stagedSQL))
	if catalog[Filename] != hex.EncodeToString(sum[:]) {
		return fmt.Errorf("staged processing schema differs from catalog")
	}
	return nil
}
func begin(ctx context.Context, pool *pgxpool.Pool, readOnly bool) (pgx.Tx, error) {
	opts := pgx.TxOptions{}
	if readOnly {
		opts.AccessMode = pgx.ReadOnly
	}
	tx, err := pool.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='5s';SET LOCAL statement_timeout='30s'"); err == nil {
		_, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLock)
	}
	if err != nil {
		tx.Rollback(context.Background())
		return nil, fmt.Errorf("bounded processing schema lock unavailable")
	}
	return tx, nil
}
func rollback(tx pgx.Tx) {
	c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = tx.Rollback(c)
}

// Inspect accepts only an exact 93 baseline with no staged object, or the same
// core ledger plus this exact staged94 and reviewed table shape. It never makes
// the existing exact93 operator accept94. Core catalog/shape validation remains
// the separate93 contract; Apply checks it before changing the ledger.
func Inspect(ctx context.Context, pool *pgxpool.Pool, dir string) (Inspection, error) {
	if err := checkBytes(); err != nil {
		return Inspection{}, err
	}
	tx, err := begin(ctx, pool, true)
	if err != nil {
		return Inspection{}, err
	}
	defer rollback(tx)
	var has bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE filename=$1)", Filename).Scan(&has); err != nil {
		return Inspection{}, fmt.Errorf("readable core ledger required")
	}
	if !has {
		var obj *string
		if err = tx.QueryRow(ctx, "SELECT to_regclass('ingest_processing_v1')::text").Scan(&obj); err != nil || obj != nil {
			return Inspection{}, fmt.Errorf("unrecorded processing object; no repair permitted")
		}
		// Release the migration lock/connection before the exact93 inspector takes it.
		rollback(tx)
		core, e := setupbootstrap.InspectReviewed(ctx, pool, dir)
		if e != nil || core.Status != "exact_reviewed_additive_93" {
			return Inspection{}, fmt.Errorf("exact reviewed93 core required")
		}
		return Inspection{Version: 1, Status: "processing_v1_absent_core93"}, nil
	}
	if err = inspect94(ctx, tx, dir); err != nil {
		return Inspection{}, err
	}
	return Inspection{Version: 1, Status: "exact_staged_processing_v1_94"}, nil
}

// Apply is explicitly operator-only. The outer adapter must establish the
// authorized target, stopped writers, verified backup and compatibility. Other
// DB clients are refused as an extra check, not a durable maintenance fence.
// Nothing is dropped on rollback to an old image, and no automatic downgrade
// exists. A commit error requires fresh inspection, never blind retry.
func Apply(ctx context.Context, pool *pgxpool.Pool, dir string) (Inspection, error) {
	before, err := Inspect(ctx, pool, dir)
	if err != nil {
		return Inspection{}, err
	}
	if before.Status == "exact_staged_processing_v1_94" {
		return before, nil
	}
	tx, err := begin(ctx, pool, false)
	if err != nil {
		return Inspection{}, err
	}
	defer rollback(tx)
	// Recheck under the shared migration lock, after the separate93 inspector.
	var clients int
	names, err := setupbootstrap.ReviewedNames(dir)
	if err != nil {
		return Inspection{}, err
	}
	rows, err := tx.Query(ctx, "SELECT filename FROM schema_migrations ORDER BY filename")
	if err != nil {
		return Inspection{}, err
	}
	got := []string{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return Inspection{}, err
		}
		got = append(got, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || !reflect.DeepEqual(got, names) {
		return Inspection{}, fmt.Errorf("core ledger changed; inspect before retry")
	}
	if err = setupbootstrap.Inspect93Objects(ctx, tx); err != nil {
		return Inspection{}, err
	}
	var obj *string
	if err = tx.QueryRow(ctx, "SELECT to_regclass('ingest_processing_v1')::text").Scan(&obj); err != nil || obj != nil {
		return Inspection{}, fmt.Errorf("processing object already exists; inspect before retry")
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND backend_type='client backend'`).Scan(&clients); err != nil || clients != 0 {
		return Inspection{}, fmt.Errorf("other database clients present or unverifiable")
	}
	if _, err = tx.Exec(ctx, stagedSQL); err != nil {
		return Inspection{}, fmt.Errorf("staged processing schema failed; not committed")
	}
	if _, err = tx.Exec(ctx, "INSERT INTO schema_migrations(filename) VALUES($1)", Filename); err != nil {
		return Inspection{}, fmt.Errorf("staged processing ledger append failed; not committed")
	}
	if err = inspect94(ctx, tx, dir); err != nil {
		return Inspection{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Inspection{}, fmt.Errorf("processing schema commit uncertain; inspect exact ledger and shape before retry")
	}
	return Inspection{Version: 1, Status: "exact_staged_processing_v1_94"}, nil
}

func inspect94(ctx context.Context, tx pgx.Tx, dir string) error {
	// Compare ledger with exact packaged93 names, not a count-only acceptance.
	// Read the catalog through the same core validator used by exact93. Its
	// exported name listing verifies every packaged byte without applying DDL.
	names, err := setupbootstrap.ReviewedNames(dir)
	if err != nil {
		return err
	}
	want := map[string]bool{Filename: true}
	for _, name := range names {
		want[name] = true
	}
	rows, err := tx.Query(ctx, "SELECT filename FROM schema_migrations")
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for rows.Next() {
		var n string
		if err = rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		if !want[n] || have[n] {
			rows.Close()
			return fmt.Errorf("unknown processing ledger history")
		}
		have[n] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(have) != len(want) {
		return fmt.Errorf("incomplete processing ledger")
	}
	if err = setupbootstrap.Inspect93Objects(ctx, tx); err != nil {
		return err
	}
	var schema string
	if err = tx.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		return err
	}
	rows, err = tx.Query(ctx, shapeSQL, []string{"ingest_processing_v1"})
	if err != nil {
		return err
	}
	actual := []any{}
	for rows.Next() {
		var kind, relation, name, raw string
		if err = rows.Scan(&kind, &relation, &name, &raw); err != nil {
			rows.Close()
			return err
		}
		var d map[string]any
		if json.Unmarshal([]byte(raw), &d) != nil {
			rows.Close()
			return fmt.Errorf("unknown processing schema object")
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
		actual = append(actual, map[string]any{"kind": kind, "relation": relation, "name": name, "definition": d})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var expected []any
	if json.Unmarshal(expectedShapeJSON, &expected) != nil || !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("staged processing schema shape differs; deployment blocked")
	}
	return nil
}
