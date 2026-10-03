package flow

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGContext stores flow and global context in Postgres, scoped to one tenant.
// Incr is atomic, so two runs in parallel never lose an update.
type PGContext struct {
	Pool   *pgxpool.Pool
	Tenant string
}

func (c *PGContext) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 3*time.Second)
}

func (c *PGContext) Get(scope, key string) (float64, bool, error) {
	ctx, cancel := c.ctx()
	defer cancel()
	var v float64
	err := c.Pool.QueryRow(ctx, `SELECT value FROM flow_context WHERE tenant_id=$1 AND scope=$2 AND key=$3`, c.Tenant, scope, key).Scan(&v)
	if err == pgx.ErrNoRows {
		return 0, false, nil
	}
	return v, err == nil, err
}

// room enforces the per-scope key cap before a new key is created.
func (c *PGContext) room(ctx context.Context, scope, key string) error {
	var n int
	var exists bool
	if err := c.Pool.QueryRow(ctx, `SELECT count(*), COALESCE(bool_or(key=$3), false) FROM flow_context WHERE tenant_id=$1 AND scope=$2`, c.Tenant, scope, key).Scan(&n, &exists); err != nil {
		return err
	}
	if !exists && n >= maxContextKeys {
		return fmt.Errorf("too many context keys in this scope (max %d)", maxContextKeys)
	}
	return nil
}

func (c *PGContext) Set(scope, key string, v float64) error {
	ctx, cancel := c.ctx()
	defer cancel()
	if err := c.room(ctx, scope, key); err != nil {
		return err
	}
	_, err := c.Pool.Exec(ctx, `INSERT INTO flow_context(tenant_id,scope,key,value) VALUES($1,$2,$3,$4)
		ON CONFLICT (tenant_id,scope,key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()`, c.Tenant, scope, key, v)
	return err
}

func (c *PGContext) Incr(scope, key string, d float64) (float64, error) {
	ctx, cancel := c.ctx()
	defer cancel()
	if err := c.room(ctx, scope, key); err != nil {
		return 0, err
	}
	var v float64
	err := c.Pool.QueryRow(ctx, `INSERT INTO flow_context(tenant_id,scope,key,value) VALUES($1,$2,$3,$4)
		ON CONFLICT (tenant_id,scope,key) DO UPDATE SET value=flow_context.value+EXCLUDED.value, updated_at=now()
		RETURNING value`, c.Tenant, scope, key, d).Scan(&v)
	if err == nil && (math.IsInf(v, 0) || math.IsNaN(v)) {
		return 0, fmt.Errorf("value overflow")
	}
	return v, err
}
