// Package pairingproof records a short-lived nonce received on an own-gateway
// broker topic. No command, subscription request, source or reading is created.
package pairingproof

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

var nonceRE = regexp.MustCompile(`^[a-f0-9]{64}$`)

func Hash(nonce string) string {
	sum := sha256.Sum256([]byte(nonce))
	return hex.EncodeToString(sum[:])
}

// Observe must only be wired to the certificate-only broker ingest connection.
// The broker enforces which certificate can publish this exact topic. A lab
// anonymous listener cannot provide this evidence, so its handler stays disabled.
func Observe(ctx context.Context, pool *pgxpool.Pool, topic string, payload []byte) (bool, error) {
	parts := strings.Split(topic, "/")
	if len(parts) != 6 || parts[0] != "t" || parts[2] != "g" || parts[4] != "pairing" || parts[5] != "proof" || parts[1] == "" || parts[3] == "" || len(payload) > 512 {
		return false, fmt.Errorf("invalid pairing proof topic or size")
	}
	var in struct {
		ID    string `json:"id"`
		Nonce string `json:"nonce"`
	}
	if json.Unmarshal(payload, &in) != nil || len(in.ID) > 64 || in.ID == "" || !nonceRE.MatchString(in.Nonce) {
		return false, fmt.Errorf("invalid pairing proof")
	}
	tag, err := pool.Exec(ctx, `UPDATE edge_pairing_proofs p SET observed_at=now()
 FROM gateways g,edge_identity_migrations m
 WHERE p.id=$1 AND p.nonce_sha256=$2 AND p.tenant_id=$3 AND p.gateway_id=$4
 AND p.observed_at IS NULL AND p.expires_at>now()
 AND g.id=p.gateway_id AND g.tenant_id=p.tenant_id AND g.status='active'
 AND g.cert_fingerprint=p.cert_fingerprint
 AND m.id=p.migration_id AND m.gateway_id=g.id AND m.tenant_id=g.tenant_id
 AND m.state='issued' AND m.cert_fingerprint=g.cert_fingerprint AND m.serial=g.serial`, in.ID, Hash(in.Nonce), parts[1], parts[3])
	return tag.RowsAffected() == 1, err
}
