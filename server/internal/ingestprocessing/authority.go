package ingestprocessing

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

var ErrDurableDenial = errors.New("staged durable denial unavailable or leaf denied; retain original input")

// DenyLeaf durably denies an exact full-leaf SHA256 for callers using the
// separately configured durable transport. It grants nothing and authenticates
// nobody: a future controller MUST separately authenticate/authorize the owner.
// No runtime or management endpoint invokes it. Requires separately installed95.
// A successful return confirms the transaction, NOT response delivery or provider
// rollback. Commit failure is uncertain: inspect before another decision. No retry.
// Already-started guarded transactions serialize BEFORE this denial can commit.
// Legacy non-durable constructors, direct DB writers and restored older backups
// are NOT protected. Preserve this authority through restore before activation.
func (x Engine) DenyLeaf(ctx context.Context, leaf [32]byte) error {
	if !x.Enabled {
		return ErrDisabled
	}
	if ctx == nil || x.Pool == nil || leaf == ([32]byte{}) {
		return ErrDurableDenial
	}
	tx, e := x.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer rollback(tx)
	if _, e = tx.Exec(ctx, "SET LOCAL lock_timeout='5s';SET LOCAL statement_timeout='15s'"); e != nil {
		return e
	}
	var rev int64
	// Same row is held FOR SHARE by every guarded admission until commit. Updating
	// it also forces a serializable waiter with an old snapshot to fail, not miss
	// an already-committed denial after waiting on the global admission lock.
	if e = tx.QueryRow(ctx, "UPDATE admission_denial_epoch_v1 SET revision=revision+1 WHERE singleton=true RETURNING revision").Scan(&rev); e != nil {
		return ErrDurableDenial
	}
	if _, e = tx.Exec(ctx, "INSERT INTO admission_denied_leaf_v1(leaf_sha256) VALUES($1) ON CONFLICT(leaf_sha256) DO NOTHING", leaf[:]); e != nil {
		return ErrDurableDenial
	}
	if e = tx.Commit(ctx); e != nil {
		return errors.New("durable leaf denial commit uncertain; inspect authority before deciding")
	}
	return nil
}
func guardDurableLeaf(ctx context.Context, tx pgx.Tx, leaf [32]byte) error {
	if leaf == ([32]byte{}) {
		return ErrDurableDenial
	}
	var rev int64
	if e := tx.QueryRow(ctx, "SELECT revision FROM admission_denial_epoch_v1 WHERE singleton=true FOR SHARE").Scan(&rev); e != nil || rev < 0 {
		return ErrDurableDenial
	}
	var denied bool
	if e := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM admission_denied_leaf_v1 WHERE leaf_sha256=$1)", leaf[:]).Scan(&denied); e != nil || denied {
		return ErrDurableDenial
	}
	return nil
}
