package ingestprocessing

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/authorityschema"
	"github.com/jackc/pgx/v5"
)

var ErrGuardedAdmissionServer = errors.New("staged guarded server requires exact95 and all current certificate bindings; do not activate")

// NewGuardedAdmissionServer is the separately opted-in assembly path for future
// runtime wiring. Both protections MUST be explicitly enabled. Unlike the legacy
// constructor it refuses missing exact95 or ANY currently invalid route before
// returning a listener-capable object. It never migrates, opens a listener, writes
// authority or retries. No runtime/startup currently invokes it.
//
// Preflight is point-in-time evidence, NOT lasting authorization or restore
// continuity. The actual handler keeps BOTH guards in EVERY admission/replay
// transaction, including after later gateway changes or durable denial. Schema
// inspection is separate from the binding transaction: hostile DDL/writers remain
// outside this guarantee. Old backups can pass shape checks after losing denials;
// independently preserved continuity evidence is still required before activation.
// Legacy NewAdmissionServer/HTTP and direct Engine/DB callers remain bypasses.
func NewGuardedAdmissionServer(ctx context.Context, c AdmissionServerConfig, dir string) (*AdmissionServer, error) {
	if !c.Enabled {
		return nil, ErrDisabled
	}
	if ctx == nil || !c.Admission.DurableDenials || !c.Admission.GatewayAuthority {
		return nil, ErrGuardedAdmissionServer
	}
	// Validate all TLS/signer/route bounds before touching the database. The
	// constructed handler owns detached routes, not the caller's mutable slice.
	s, err := NewAdmissionServer(c)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err = authorityschema.Inspect(ctx, s.admission.engine.Pool, dir); err != nil {
		return nil, ErrGuardedAdmissionServer
	}
	tx, err := s.admission.engine.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, ErrGuardedAdmissionServer
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='5s';SET LOCAL statement_timeout='15s'"); err != nil {
		return nil, ErrGuardedAdmissionServer
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(731492068214)"); err != nil {
		return nil, ErrGuardedAdmissionServer
	}
	leaves := make([][32]byte, 0, len(s.admission.routes))
	for leaf := range s.admission.routes {
		leaves = append(leaves, leaf)
	}
	sort.Slice(leaves, func(i, j int) bool { return bytes.Compare(leaves[i][:], leaves[j][:]) < 0 })
	for _, leaf := range leaves {
		// NewAdmissionHTTP already validated the exact topic grammar.
		parts := strings.Split(s.admission.routes[leaf], "/")
		e := Envelope{TenantID: parts[1], GatewayID: parts[3]}
		if err = guardDurableLeaf(ctx, tx, leaf); err != nil {
			return nil, ErrGuardedAdmissionServer
		}
		if err = guardGatewayAuthority(ctx, tx, e, leaf); err != nil {
			return nil, ErrGuardedAdmissionServer
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, ErrGuardedAdmissionServer
	}
	return s, nil
}
