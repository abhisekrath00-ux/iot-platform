package authorityschema

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Evidence is a deterministic read-only fingerprint of a STRICT95 denial state.
// It contains no leaf IDs or keys. It is NOT a signature, an operator grant or
// proof that an untrusted restore is current. Capture it independently BEFORE
// backup while all writers are stopped, and preserve its trusted provenance.
// A baseline copied FROM the backup being checked establishes no continuity.
// Concurrent DB writers, later denials, old baselines and a hostile DB remain
// outside the maintenance guarantee. No API/startup/activation consumes it.
type Evidence struct {
	Version           int    `json:"version"`
	Status            string `json:"status"`
	Revision          int64  `json:"revision"`
	DeniedLeaves      int64  `json:"denied_leaves"`
	SHA256            string `json:"sha256"`
	RuntimeAccepted   bool   `json:"runtime_accepted"`
	ProcessingEnabled bool   `json:"processing_enabled"`
}

// InspectEvidence shares the migration lock and the denial epoch, then reads
// sorted exact leaves/timestamps in the SAME transaction as strict inspection.
// Its fingerprint changes on additional denial operations, even repeated ones.
// This operation has bounded SQL lock/statement deadlines, no writes or retry.
func InspectEvidence(ctx context.Context, p *pgxpool.Pool, dir string) (Evidence, error) {
	if e := checkBytes(); e != nil {
		return Evidence{}, e
	}
	tx, e := begin(ctx, p)
	if e != nil {
		return Evidence{}, e
	}
	defer tx.Rollback(context.Background())
	var rev int64
	if e = tx.QueryRow(ctx, "SELECT revision FROM admission_denial_epoch_v1 WHERE singleton=true FOR SHARE").Scan(&rev); e != nil || rev < 0 {
		return Evidence{}, ErrSchema
	}
	if e = inspect(ctx, tx, dir); e != nil {
		return Evidence{}, e
	}
	rows, e := tx.Query(ctx, "SELECT leaf_sha256,denied_at FROM admission_denied_leaf_v1 ORDER BY leaf_sha256")
	if e != nil {
		return Evidence{}, e
	}
	defer rows.Close()
	h := sha256.New()
	h.Write([]byte("HexThings/denial95/logical-evidence/v1\x00"))
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(rev))
	h.Write(buf[:])
	var n int64
	for rows.Next() {
		var leaf []byte
		var at time.Time
		if e = rows.Scan(&leaf, &at); e != nil {
			return Evidence{}, e
		}
		if len(leaf) != 32 || at.Year() < 1970 || at.Year() > 9999 || !at.Equal(at.Truncate(time.Microsecond)) {
			return Evidence{}, ErrSchema
		}
		h.Write(leaf)
		binary.BigEndian.PutUint64(buf[:], uint64(at.UnixMicro()))
		h.Write(buf[:])
		n++
	}
	if e = rows.Err(); e != nil {
		return Evidence{}, e
	}
	return Evidence{Version: 1, Status: "exact_staged_denial_v1_95", Revision: rev, DeniedLeaves: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

// VerifyEvidence compares against independently preserved maintenance evidence,
// NOT a baseline recovered from the same backup. Equality is point-in-time
// denial-state continuity only, not complete restore/owner/runtime acceptance.
func VerifyEvidence(ctx context.Context, p *pgxpool.Pool, dir string, want Evidence) error {
	digest, e := hex.DecodeString(want.SHA256)
	if e != nil || len(digest) != 32 || want.SHA256 != hex.EncodeToString(digest) || want.Version != 1 || want.Status != "exact_staged_denial_v1_95" || want.Revision < 0 || want.DeniedLeaves < 0 || want.RuntimeAccepted || want.ProcessingEnabled {
		return ErrSchema
	}
	got, e := InspectEvidence(ctx, p, dir)
	if e != nil {
		return e
	}
	if got != want {
		return fmt.Errorf("denial authority continuity differs; do not activate or repair from this backup")
	}
	return nil
}
