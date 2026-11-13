package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This protocol is a read-only prerequisite, not an activation grant or a
// statement about the route, issuer, running deployment or physical gateway.
type setupCapabilities struct {
	Version             int      `json:"version"`
	Protocols           []string `json:"protocols"`
	ActivatesDeployment bool     `json:"activates_deployment"`
}

func currentSetupCapabilities() setupCapabilities {
	return setupCapabilities{Version: 1, Protocols: []string{"certificate_acl_snapshot_v1", "read_only_setup_readiness_v1"}}
}

type setupReadiness struct {
	Version            int      `json:"version"`
	Status             string   `json:"status"`
	AppliedCount       int      `json:"applied_count"`
	PackagedCount      int      `json:"packaged_count"`
	Pending            []string `json:"pending_migrations"`
	Unknown            []string `json:"unknown_migrations"`
	DeploymentApproved bool     `json:"deployment_approved"`
}

func compareSetupMigrations(packaged, applied []string) setupReadiness {
	result := setupReadiness{Version: 1, Status: "migration_ledger_current", PackagedCount: len(packaged), AppliedCount: len(applied), Pending: []string{}, Unknown: []string{}}
	have, want := map[string]bool{}, map[string]bool{}
	for _, name := range applied {
		have[name] = true
	}
	for _, name := range packaged {
		want[name] = true
		if !have[name] {
			result.Pending = append(result.Pending, name)
		}
	}
	for _, name := range applied {
		if !want[name] {
			result.Unknown = append(result.Unknown, name)
		}
	}
	sort.Strings(result.Pending)
	sort.Strings(result.Unknown)
	if len(packaged) == 0 || len(result.Pending) > 0 || len(result.Unknown) > 0 {
		result.Status = "application_bootstrap_required"
	}
	return result
}
func inspectSetupSchema(ctx context.Context, pool *pgxpool.Pool, dir string) (setupReadiness, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return setupReadiness{}, fmt.Errorf("cannot inspect packaged migration files; no setup readiness proved")
	}
	names := []string{}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return setupReadiness{}, fmt.Errorf("cannot open read-only schema inspection; no setup readiness proved")
	}
	defer tx.Rollback(ctx)
	var ledger *string
	if err = tx.QueryRow(ctx, "SELECT to_regclass('schema_migrations')::text").Scan(&ledger); err != nil {
		return setupReadiness{}, fmt.Errorf("cannot inspect migration ledger; no setup readiness proved")
	}
	applied := []string{}
	if ledger != nil {
		rows, e := tx.Query(ctx, "SELECT filename FROM schema_migrations ORDER BY filename")
		if e != nil {
			return setupReadiness{}, fmt.Errorf("cannot read migration ledger; no setup readiness proved")
		}
		for rows.Next() {
			var name string
			if e = rows.Scan(&name); e != nil {
				rows.Close()
				return setupReadiness{}, fmt.Errorf("invalid migration ledger; no setup readiness proved")
			}
			applied = append(applied, name)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return setupReadiness{}, fmt.Errorf("incomplete migration ledger; no setup readiness proved")
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return setupReadiness{}, fmt.Errorf("cannot finish read-only schema inspection; no setup readiness proved")
	}
	return compareSetupMigrations(names, applied), nil
}
func setupMigrationsDir() string {
	if dir := os.Getenv("MIGRATIONS_DIR"); dir != "" {
		return filepath.Clean(dir)
	}
	return "/migrations"
}
func printSetupJSON(value any) {
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		fatal("cannot write setup diagnostic")
	}
}
