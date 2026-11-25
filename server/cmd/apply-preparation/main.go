// apply-preparation is read-only operator preparation, not Apply activation.
// It never provisions/clears storage, installs SQL, or starts/stops services.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/applyadmission"
	"github.com/jackc/pgx/v5"
)

type report struct {
	ReviewOnly            bool   `json:"review_only"`
	PreparedFilesVerified bool   `json:"prepared_files_verified"`
	NewInstallObservation bool   `json:"new_install_observation"`
	ActivationAllowed     bool   `json:"activation_allowed"`
	ServicesChanged       bool   `json:"services_changed"`
	Reason                string `json:"reason"`
}

func inspect(ctx context.Context, dsn, directory, binding, deployment string) report {
	r := report{ReviewOnly: true, Reason: "Independent prepared storage is unavailable or mismatched. Preserve existing files; do not reset or create a replacement to retry."}
	f, err := applyadmission.OpenDeploymentFence(directory, binding, deployment)
	if err != nil {
		return r
	}
	r.PreparedFilesVerified = true
	if dsn == "" {
		r.Reason = "Database connection is not configured. No database or service was changed."
		return r
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		r.Reason = "Database could not be checked. The outcome is unknown; no service was changed."
		return r
	}
	defer conn.Close(context.Background())
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		r.Reason = "Database checks are unavailable. No activation is allowed."
		return r
	}
	defer tx.Rollback(context.Background())
	// Verify uses an explicit table lock. All statements here are observations;
	// rollback releases that lock and never installs guards or changes records.
	if err = applyadmission.PreflightNewInstall(ctx, tx, f); err != nil {
		r.Reason = "New-install safety checks did not pass. Existing history, an unavailable guard or changed storage needs administrator review. Do not reset history or use the legacy updater as fallback."
		return r
	}
	r.NewInstallObservation = true
	r.Reason = "Prepared files and empty-history checks passed at this observation only. This is not writer quiescence, deployment approval, device opt-in or readiness to Apply. Product activation is unavailable."
	return r
}
func run(args []string, getenv func(string) string, out, errors io.Writer) int {
	fs := flag.NewFlagSet("apply-preparation", flag.ContinueOnError)
	fs.SetOutput(errors)
	directory := fs.String("store", "", "absolute existing retained store")
	binding := fs.String("binding", "", "absolute independent existing binding file")
	deployment := fs.String("deployment", "", "expected deployment identity")
	machine := fs.Bool("json", false, "redacted read-only report")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *directory == "" || *binding == "" || *deployment == "" {
		fmt.Fprintln(errors, "Existing store, independent binding and deployment are required. No files were created.")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r := inspect(ctx, getenv("DATABASE_URL"), *directory, *binding, *deployment)
	if *machine {
		if err := json.NewEncoder(out).Encode(r); err != nil {
			return 1
		}
	} else {
		fmt.Fprintln(out, "Apply preparation review. No files or services changed.")
		fmt.Fprintln(out, r.Reason)
	}
	if !r.NewInstallObservation {
		return 1
	}
	return 0
}
func main() { os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr)) }
