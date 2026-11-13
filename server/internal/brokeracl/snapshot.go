package brokeracl

import (
	"context"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SnapshotCertificateACL reads the whole server, not one workspace. It is a
// local operator operation, never a public API. Password-auth direct devices
// are refused because the private certificate-only listener cannot preserve
// their authentication. No gateway row or existing broker file is changed.
func SnapshotCertificateACL(ctx context.Context, pool *pgxpool.Pool) (string, int, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback(ctx)
	var unsupported int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM gateways WHERE status='active' AND kind='direct' AND auth_mode='password'`).Scan(&unsupported); err != nil {
		return "", 0, err
	}
	if unsupported > 0 {
		return "", 0, fmt.Errorf("certificate-only deployment cannot preserve %d active password-auth direct devices; no ACL exported", unsupported)
	}
	rows, err := tx.Query(ctx, `SELECT serial,tenant_id,id,kind='direct' FROM gateways WHERE status='active' AND cert_fingerprint IS NOT NULL AND cert_fingerprint<>'' ORDER BY serial`)
	if err != nil {
		return "", 0, err
	}
	var entries []Entry
	seen := map[string]bool{}
	for rows.Next() {
		var e Entry
		if err = rows.Scan(&e.Serial, &e.TenantID, &e.GatewayID, &e.Direct); err != nil {
			rows.Close()
			return "", 0, err
		}
		if !safeACLIdentity(e.Serial) || !safeACLIdentity(e.TenantID) || !safeACLIdentity(e.GatewayID) || e.Serial == "api" || e.Serial == "ingest" || seen[e.Serial] {
			rows.Close()
			return "", 0, fmt.Errorf("unsafe or conflicting gateway identity; no ACL exported")
		}
		seen[e.Serial] = true
		entries = append(entries, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", 0, err
	}
	return Generate(entries), len(entries), nil
}

var aclIdentityRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func safeACLIdentity(s string) bool { return aclIdentityRE.MatchString(s) }
