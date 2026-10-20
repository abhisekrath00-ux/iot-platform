// Package migrate applies the SQL migration files in filename order. Every
// applied file is recorded in schema_migrations and never run again, so an
// already-current database is left completely untouched: booting a second
// replica or a fresh test server against a migrated database takes NO DDL
// locks. That matters beyond tidiness - a no-op ALTER TABLE still takes an
// ACCESS EXCLUSIVE lock, and a migration pass holding that lock while it
// reaches for a second table's lock can deadlock against an ordinary request
// (observed in CI: migration 0011 holding alerts and wanting alert_comments
// vs a comment INSERT holding alert_comments and wanting alerts).
//
// Each file still runs as one implicit transaction and must stay idempotent:
// a file whose apply or ledger write fails is not recorded and will run
// again on the next boot.
package migrate

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// advisoryLockID serializes schema migrations across every process using
// this database (API replicas, test packages). Same key the API has always used.
const advisoryLockID int64 = 0x4845584D4F4E // "HEXMON"

// Apply runs every migration file in dir that is not yet recorded in
// schema_migrations, in filename order, and records each one it applies.
func Apply(ctx context.Context, pool *pgxpool.Pool, dir string) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockID); err != nil {
		return err
	}
	defer func() {
		// the boot context may already be cancelled; unlock on a fresh one
		conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", advisoryLockID) //nolint:errcheck
	}()
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		filename TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("schema_migrations: %w", err)
	}
	rows, err := conn.Query(ctx, `SELECT filename FROM schema_migrations`)
	if err != nil {
		return err
	}
	applied := map[string]bool{}
	for rows.Next() {
		var f string
		if rows.Scan(&f) == nil {
			applied[f] = true
		}
	}
	rows.Close()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	names := []string{}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if applied[name] {
			continue
		}
		if err := applyOne(ctx, conn, dir, name); err != nil {
			return err
		}
	}
	return nil
}

func applyOne(ctx context.Context, conn *pgxpool.Conn, dir, name string) error {
	b, err := os.ReadFile(dir + "/" + name)
	if err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, string(b)); err != nil {
		return fmt.Errorf("migration %s: %w", name, err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO schema_migrations(filename) VALUES($1) ON CONFLICT DO NOTHING`, name); err != nil {
		return fmt.Errorf("record migration %s: %w", name, err)
	}
	log.Printf("migration applied: %s", name)
	return nil
}

// ApplyFile forces one named migration file through again, ignoring the
// ledger. Tests use it to prove a migration's content stays idempotent;
// normal boots never re-run recorded files.
func ApplyFile(ctx context.Context, pool *pgxpool.Pool, dir, name string) error {
	b, err := os.ReadFile(dir + "/" + name)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, string(b))
	return err
}
