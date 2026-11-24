package applyadmission

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
)

// PreflightNewInstall is a conservative observation, NOT activation or an
// adoption lease. All writers must be quiesced separately before adoption.
// Existing history needs explicit reconciliation, even if its rows are terminal.
// This never installs SQL, provisions storage, or clears existing history.
func PreflightNewInstall(ctx context.Context, tx pgx.Tx, fence *Fence) error {
	if err := Verify(ctx, tx); err != nil {
		return err
	}
	if err := fence.RequireBound(); err != nil {
		return err
	}
	root, err := fence.root("preflight", "new-install")
	if err != nil {
		return err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return err
	}
	if len(entries) != 1 || entries[0].Name() != "store.identity" {
		return fmt.Errorf("existing retained history requires explicit reconciliation")
	}
	var rows int64
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM gateway_config_pushes").Scan(&rows); err != nil {
		return err
	}
	if rows != 0 {
		return fmt.Errorf("existing database Apply history requires explicit reconciliation")
	}
	return fence.check("preflight", "new-install")
}
