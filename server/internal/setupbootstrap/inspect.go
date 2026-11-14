package setupbootstrap

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema-shape.sql
var shapeQuery string

//go:embed reviewed-schema-shape.json
var reviewedShapeJSON []byte

type shapeItem struct {
	Kind       string         `json:"kind"`
	Relation   string         `json:"relation"`
	Name       string         `json:"name"`
	Definition map[string]any `json:"definition"`
}

type Inspection struct {
	Version         int    `json:"version"`
	Status          string `json:"status"`
	RuntimeAccepted bool   `json:"runtime_accepted"`
}

// InspectReviewed is read-only and bounded. It checks exact packaged bytes,
// ledger filenames and the four added objects' columns/defaults/constraints/
// indexes, not the full historical schema, image compatibility or authority.
// Unknown/partial history or shape is an error, never a rollback permission.
func InspectReviewed(ctx context.Context, pool *pgxpool.Pool, dir string) (Inspection, error) {
	files, err := catalog(dir)
	if err != nil {
		return Inspection{}, err
	}
	// READ COMMITTED is intentional: a snapshot captured while waiting for
	// another migrator could otherwise report its pre-commit ledger after the
	// lock is acquired. All supported migrators hold this same exclusive lock.
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Inspection{}, fmt.Errorf("cannot open read-only reviewed schema inspection")
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='5s'; SET LOCAL statement_timeout='30s'"); err != nil {
		return Inspection{}, fmt.Errorf("cannot bound schema inspection")
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", lockID); err != nil {
		return Inspection{}, fmt.Errorf("cannot lock schema inspection within bounded wait")
	}
	var schema string
	if err = tx.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil || schema == "" {
		return Inspection{}, fmt.Errorf("exact current schema required")
	}
	rows, err := tx.Query(ctx, "SELECT filename FROM schema_migrations ORDER BY filename")
	if err != nil {
		return Inspection{}, fmt.Errorf("existing readable migration ledger required")
	}
	have := map[string]bool{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return Inspection{}, fmt.Errorf("invalid migration ledger")
		}
		if have[name] || files[name] == nil {
			rows.Close()
			return Inspection{}, fmt.Errorf("unknown or duplicate migration history")
		}
		have[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Inspection{}, fmt.Errorf("incomplete migration history")
	}
	missing := map[string]bool{}
	for name := range files {
		if !have[name] {
			missing[name] = true
		}
	}
	baseline := len(missing) == len(added)
	for _, name := range added {
		if !missing[name] {
			baseline = false
		}
	}
	if len(missing) != 0 && !baseline {
		return Inspection{}, fmt.Errorf("partial or unknown migration history")
	}
	actual, err := readReviewedShape(ctx, tx, schema)
	if err != nil {
		return Inspection{}, err
	}
	if baseline {
		if len(actual) != 0 {
			return Inspection{}, fmt.Errorf("unrecorded additive schema objects exist")
		}
		return Inspection{Version: 1, Status: "exact_reviewed_baseline_89"}, nil
	}
	var expected []shapeItem
	if err = json.Unmarshal(reviewedShapeJSON, &expected); err != nil || !reflect.DeepEqual(actual, expected) {
		return Inspection{}, fmt.Errorf("reviewed additive schema shape differs; keep deployment blocked")
	}
	return Inspection{Version: 1, Status: "exact_reviewed_additive_93"}, nil
}

// Inspect93Objects verifies only the existing reviewed93 additions on the
// caller's transaction. It does not validate or widen any migration ledger.
// Separate staged schema operators combine it with their own exact catalog.
func Inspect93Objects(ctx context.Context, tx pgx.Tx) error {
	var schema string
	if err := tx.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil || schema == "" {
		return fmt.Errorf("exact current schema required")
	}
	actual, err := readReviewedShape(ctx, tx, schema)
	if err != nil {
		return err
	}
	var expected []shapeItem
	if json.Unmarshal(reviewedShapeJSON, &expected) != nil || !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("reviewed93 object shape differs")
	}
	return nil
}
func readReviewedShape(ctx context.Context, tx pgx.Tx, schema string) ([]shapeItem, error) {
	rows, err := tx.Query(ctx, shapeQuery, []string{"edge_identity_migrations", "edge_serial_port_inventory", "edge_pairing_proofs"})
	if err != nil {
		return nil, fmt.Errorf("cannot inspect reviewed schema shape")
	}
	actual := []shapeItem{}
	for rows.Next() {
		var item shapeItem
		var definition string
		if err = rows.Scan(&item.Kind, &item.Relation, &item.Name, &definition); err != nil {
			rows.Close()
			return nil, fmt.Errorf("invalid reviewed schema shape")
		}
		if err = json.Unmarshal([]byte(definition), &item.Definition); err != nil {
			rows.Close()
			return nil, fmt.Errorf("invalid schema definition")
		}
		if item.Kind == "index" {
			// pg_get_indexdef qualifies the indexed table. Normalize only that
			// first syntactic ON target, never arbitrary schema-like text in
			// defaults, CHECK values, functions or index predicates.
			text, ok := item.Definition["definition"].(string)
			if !ok {
				rows.Close()
				return nil, fmt.Errorf("invalid index definition")
			}
			for _, namespace := range []string{pgx.Identifier{schema}.Sanitize(), schema} {
				for _, table := range []string{pgx.Identifier{item.Relation}.Sanitize(), item.Relation} {
					text = strings.Replace(text, " ON "+namespace+"."+table+" ", " ON "+item.Relation+" ", 1)
				}
			}
			item.Definition["definition"] = text
		}
		actual = append(actual, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("incomplete schema shape inspection")
	}
	return actual, nil
}
