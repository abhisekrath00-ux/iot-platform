// setupctl is a local-only schema operator for the application transaction
// adapter. It is not run during API startup and never activates transport,
// switches application images, restores data or supplies owner authority.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/setupbootstrap"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/setupconnection"
	"github.com/jackc/pgx/v5/pgxpool"
)

func run(ctx context.Context, args []string, input io.Reader, output io.Writer) error {
	if len(args) == 1 && (args[0] == "inspect-staged-processing" || args[0] == "apply-staged-processing") {
		return runProcessing(ctx, args[0] == "apply-staged-processing", input, output)
	}
	if len(args) == 1 && args[0] == "connection-readiness" {
		return setupconnection.Run(ctx, input, output)
	}
	if len(args) != 1 || (args[0] != "inspect-reviewed" && args[0] != "apply-reviewed") {
		return fmt.Errorf("usage: setupctl inspect-reviewed|apply-reviewed; local transaction adapter only")
	}
	if args[0] == "apply-reviewed" {
		// This is an effect acknowledgement, never authority. Only the outer
		// adapter can establish target permission, exact images, backup and stopped
		// writers. No generic --yes switch or automatic startup invocation exists.
		var request struct {
			Version        int    `json:"version"`
			Scope          string `json:"scope"`
			WritersStopped bool   `json:"writers_stopped"`
			BackupVerified bool   `json:"backup_verified"`
		}
		decoder := json.NewDecoder(io.LimitReader(input, 4097))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			return fmt.Errorf("application transaction acknowledgement required on stdin")
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF || request.Version != 1 || request.Scope != "application_bootstrap" || !request.WritersStopped || !request.BackupVerified {
			return fmt.Errorf("exact application transaction acknowledgement required")
		}
	}
	if os.Getenv("DATABASE_URL") == "" {
		return fmt.Errorf("explicit database configuration required")
	}
	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("database configuration unavailable")
	}
	cfg.MaxConns = 1
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.ConnConfig.RuntimeParams["application_name"] = "hexthings-reviewed-schema-operator"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("cannot open schema operator")
	}
	defer pool.Close()
	dir := os.Getenv("MIGRATIONS_DIR")
	if dir == "" {
		dir = "/migrations"
	}
	before, err := setupbootstrap.InspectReviewed(ctx, pool, dir)
	if err != nil {
		return err
	}
	if args[0] == "inspect-reviewed" {
		return json.NewEncoder(output).Encode(before)
	}
	// Detect other database clients after the adapter stopped Docker writers.
	// This is an extra refusal check, not proof that an external process cannot
	// reconnect. The production caller must establish that maintenance boundary.
	var other int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND backend_type='client backend'`).Scan(&other)
	if err != nil || other != 0 {
		return fmt.Errorf("other database clients are present or unverifiable; no reviewed schema change")
	}
	if _, err = setupbootstrap.ApplyReviewed(ctx, pool, dir); err != nil {
		return err
	}
	after, err := setupbootstrap.InspectReviewed(ctx, pool, dir)
	if err != nil {
		return err
	}
	if after.Status != "exact_reviewed_additive_93" {
		return fmt.Errorf("schema commit requires explicit inspection; no runtime acceptance proved")
	}
	return json.NewEncoder(output).Encode(after)
}
func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
