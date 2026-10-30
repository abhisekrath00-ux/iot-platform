package oidcstate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres shares single-use OAuth state through the existing database. It is
// independent of per-replica caches and uses the database clock for expiry.
// Store outage fails closed; it never falls back to process-local state.
type Postgres struct{ pool *pgxpool.Pool }

func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

func stateHash(state string) string {
	sum := sha256.Sum256([]byte(state))
	return hex.EncodeToString(sum[:])
}

func (p *Postgres) Put(state, nonce string, ttl time.Duration) error {
	if state == "" || nonce == "" || len(state) > 4096 || len(nonce) > 16384 || ttl <= 0 || ttl > 30*time.Minute {
		return errors.New("invalid OAuth state or lifetime")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Limit garbage collection work per login. Only expired rows are removed;
	// active state on another API replica must remain intact.
	if _, err := p.pool.Exec(ctx, `DELETE FROM oauth_states WHERE state_hash IN
  (SELECT state_hash FROM oauth_states WHERE expires_at <= clock_timestamp() ORDER BY expires_at LIMIT 1000)`); err != nil {
		return err
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO oauth_states(state_hash,nonce,expires_at)
  VALUES($1,$2,clock_timestamp()+($3::bigint * interval '1 microsecond'))`, stateHash(state), nonce, ttl.Microseconds())
	return err
}

func (p *Postgres) Take(state string) (string, bool) {
	if state == "" || len(state) > 4096 {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var nonce string
	// DELETE RETURNING is atomic across pools/processes: only one callback wins.
	err := p.pool.QueryRow(ctx, `DELETE FROM oauth_states WHERE state_hash=$1
  RETURNING CASE WHEN expires_at > clock_timestamp() THEN nonce ELSE '' END`, stateHash(state)).Scan(&nonce)
	if err != nil || nonce == "" {
		return "", false
	}
	return nonce, true
}
