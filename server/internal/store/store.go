// Package store wraps Postgres access for the control plane.
package store

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ Pool *pgxpool.Pool }

func Connect(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("pg config: %w", err)
	}
	// Each leader-elected background job holds one connection for its whole
	// life (advisory lock). pgx defaults to max(4, NumCPU), which a small host
	// exhausts with no connection left for requests, so every request hangs.
	if !strings.Contains(url, "pool_max_conns") {
		cfg.MaxConns = PoolSize(os.Getenv("DB_MAX_CONNS"), runtime.NumCPU())
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pg connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("pg ping: %w", err)
	}
	return &Store{Pool: pool}, nil
}

// MinPoolSize leaves room for every leader job (they pin connections) plus
// request traffic. Keep it above the number of leader.Run jobs in cmd/api.
const MinPoolSize = 20

// PoolSize returns DB_MAX_CONNS when it is a sane number, else the larger of
// MinPoolSize and 4 per CPU. A value below MinPoolSize is raised to it.
func PoolSize(env string, cpus int) int32 {
	n := 4 * cpus
	if v, err := strconv.Atoi(strings.TrimSpace(env)); err == nil && v > 0 {
		n = v
	}
	if n < MinPoolSize {
		n = MinPoolSize
	}
	return int32(n)
}

func (s *Store) Close() { s.Pool.Close() }

// InsertTelemetry dedupes on event_id (idempotent replay).
func (s *Store) InsertTelemetry(ctx context.Context, e Telemetry) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO telemetry
		(event_id,tenant_id,site_id,gateway_id,device_id,point_id,observed_at,value,unit,quality,schema_version)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (event_id, observed_at) DO NOTHING`,
		e.EventID, e.TenantID, e.SiteID, e.GatewayID, e.DeviceID, e.PointID,
		e.ObservedAt, e.Value, e.Unit, e.Quality, e.SchemaVersion)
	return err
}

type Telemetry struct {
	EventID, TenantID, SiteID, GatewayID, DeviceID, PointID string
	ObservedAt                                              any
	Value                                                   float64
	Unit, Quality                                           string
	SchemaVersion                                           int
}
