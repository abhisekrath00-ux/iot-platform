package ingestprocessing

import (
	"context"
	"encoding/hex"
	"errors"

	"github.com/jackc/pgx/v5"
)

var ErrGatewayAuthority = errors.New("staged current gateway identity unavailable; retain original input")

// guardGatewayAuthority binds the verified TLS leaf and frozen route to the
// EXISTING exact tenant/gateway enrollment record in the SAME serializable
// admission transaction. It writes/grants nothing. The row lock lasts through
// admission commit, including exact replay lookup. A concurrent status/fingerprint
// update either precedes admission (old snapshots serialize-fail) or waits until
// that transaction ends. Earlier signed responses may arrive after the update.
// This is not owner/admin authentication, certificate issuance/rotation, global
// fingerprint uniqueness, broker ACL replacement or protection from hostile DB
// writers. No legacy constructor is silently upgraded to use it.
func guardGatewayAuthority(ctx context.Context, tx pgx.Tx, e Envelope, leaf [32]byte) error {
	if leaf == ([32]byte{}) {
		return ErrGatewayAuthority
	}
	var status, kind, mode, fp string
	err := tx.QueryRow(ctx, `SELECT status,kind,auth_mode,COALESCE(cert_fingerprint,'') FROM gateways WHERE id=$1 AND tenant_id=$2 FOR SHARE`, e.GatewayID, e.TenantID).Scan(&status, &kind, &mode, &fp)
	if err != nil || status != "active" || kind != "edge" || mode != "cert" || fp != hex.EncodeToString(leaf[:]) {
		return ErrGatewayAuthority
	}
	return nil
}
