// Package setupbootstrap is a narrowly reviewed additive-schema engine, not an
// operator interface. There is deliberately no CLI or production caller yet.
// A future adapter must own current target/authority, exact app images, verified
// backup, every stopped writer and old/new runtime acceptance before calling it.
package setupbootstrap

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed reviewed-migrations.json
var reviewedJSON []byte

const lockID int64 = 0x4845584D4F4E // same migration lock as ordinary API startup

var added = []string{"0090_profile_connection_defaults.sql", "0091_edge_identity_migrations.sql", "0092_edge_serial_port_inventory.sql", "0093_edge_pairing_proofs.sql"}

type Result struct {
	Status  string
	Applied []string
	// This result never claims existing process compatibility or owner approval.
	RuntimeAccepted bool
}

func catalog(dir string) (map[string][]byte, error) {
	var expected map[string]string
	if err := json.Unmarshal(reviewedJSON, &expected); err != nil {
		return nil, fmt.Errorf("invalid reviewed bootstrap catalog")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("cannot read migration catalog")
	}
	result := map[string][]byte{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		info, statErr := entry.Info()
		if statErr != nil || !info.Mode().IsRegular() || entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("migration catalog must contain exact regular files")
		}
		want, ok := expected[entry.Name()]
		if !ok {
			return nil, fmt.Errorf("unknown migration; this reviewed bootstrap does not apply")
		}
		body, e := os.ReadFile(filepath.Join(dir, entry.Name()))
		if e != nil {
			return nil, fmt.Errorf("cannot read exact reviewed migration")
		}
		digest := sha256.Sum256(body)
		if hex.EncodeToString(digest[:]) != want {
			return nil, fmt.Errorf("migration content differs from reviewed bootstrap")
		}
		result[entry.Name()] = body
	}
	if len(result) != len(expected) {
		return nil, fmt.Errorf("reviewed migration catalog is incomplete")
	}
	return result, nil
}

// ApplyReviewed applies only the four reviewed additive files to an exact
// 0001-0089 ledger. It does not create a missing ledger, run arbitrary SQL files,
// update applications, export ACLs or restore DB data. All four files and their
// ledger rows share one transaction, unlike ordinary startup's per-file loop.
// Ledger names alone cannot prove older schema shape; real runtime acceptance
// remains the adapter's separate obligation. No production caller is supplied.
func ApplyReviewed(ctx context.Context, pool *pgxpool.Pool, dir string) (Result, error) {
	files, err := catalog(dir)
	if err != nil {
		return Result{}, err
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Result{}, fmt.Errorf("cannot open reviewed bootstrap transaction")
	}
	defer tx.Rollback(ctx)
	// Set bounds before acquiring the shared advisory lock. Setting them after
	// the lock would leave an operator waiting forever behind another migrator.
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='5s'; SET LOCAL statement_timeout='30s'"); err != nil {
		return Result{}, fmt.Errorf("cannot bound bootstrap lock and statement waits")
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", lockID); err != nil {
		return Result{}, fmt.Errorf("cannot lock reviewed bootstrap within bounded wait; no bootstrap performed")
	}
	var ledger *string
	if err = tx.QueryRow(ctx, "SELECT to_regclass('schema_migrations')::text").Scan(&ledger); err != nil || ledger == nil {
		return Result{}, fmt.Errorf("existing migration ledger required; no bootstrap performed")
	}
	rows, err := tx.Query(ctx, "SELECT filename FROM schema_migrations ORDER BY filename")
	if err != nil {
		return Result{}, fmt.Errorf("cannot inspect existing migration ledger")
	}
	have := map[string]bool{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return Result{}, fmt.Errorf("invalid migration ledger")
		}
		if have[name] {
			rows.Close()
			return Result{}, fmt.Errorf("duplicate migration history; no bootstrap performed")
		}
		have[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Result{}, fmt.Errorf("incomplete migration ledger")
	}
	for name := range have {
		if _, ok := files[name]; !ok {
			return Result{}, fmt.Errorf("unknown existing migration history; no bootstrap performed")
		}
	}
	missing := []string{}
	for name := range files {
		if !have[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) == 0 {
		return Result{Status: "already_recorded_not_runtime_accepted", Applied: []string{}}, nil
	}
	if strings.Join(missing, "\n") != strings.Join(added, "\n") {
		return Result{}, fmt.Errorf("only exact reviewed 0001-0089 baseline is supported; no bootstrap performed")
	}
	// Refuse objects that IF NOT EXISTS would otherwise silently accept without
	// proving their shape. These are additions, never replacements or repairs.
	for _, name := range []string{"edge_identity_migrations", "edge_serial_port_inventory", "edge_pairing_proofs"} {
		var found *string
		if err = tx.QueryRow(ctx, "SELECT to_regclass($1)::text", name).Scan(&found); err != nil || found != nil {
			return Result{}, fmt.Errorf("unrecorded bootstrap object already exists; schema review required")
		}
	}
	var columnExists bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='device_profiles' AND column_name='connection_defaults')").Scan(&columnExists); err != nil || columnExists {
		return Result{}, fmt.Errorf("unrecorded connection defaults column exists; schema review required")
	}
	for _, name := range added {
		if _, err = tx.Exec(ctx, string(files[name])); err != nil {
			return Result{}, fmt.Errorf("reviewed additive bootstrap failed; transaction not committed")
		}
		if _, err = tx.Exec(ctx, "INSERT INTO schema_migrations(filename) VALUES($1)", name); err != nil {
			return Result{}, fmt.Errorf("bootstrap ledger append failed; transaction not committed")
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("reviewed bootstrap commit is unverified; inspect ledger before any retry")
	}
	return Result{Status: "additive_schema_committed_not_runtime_accepted", Applied: append([]string{}, added...)}, nil
}
