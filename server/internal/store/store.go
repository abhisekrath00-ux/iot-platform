// Package store wraps Postgres access for the control plane.
package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ Pool *pgxpool.Pool }

func Connect(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("pg connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("pg ping: %w", err)
	}
	return &Store{Pool: pool}, nil
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
