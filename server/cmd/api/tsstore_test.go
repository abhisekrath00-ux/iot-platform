package main

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/tsstore"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/tsstore/storetest"
)

type pgLoader struct{ pool *pgxpool.Pool }

func (l pgLoader) Reset(ctx context.Context, tenants ...string) error {
	for _, t := range tenants {
		for _, q := range []string{
			`DELETE FROM telemetry WHERE tenant_id=$1`, `DELETE FROM telemetry_rollup_hourly WHERE tenant_id=$1`,
			`DELETE FROM telemetry_rollup_daily WHERE tenant_id=$1`, `DELETE FROM points WHERE device_id IN (SELECT id FROM devices WHERE tenant_id=$1)`,
			`DELETE FROM devices WHERE tenant_id=$1`, `DELETE FROM gateways WHERE tenant_id=$1`, `DELETE FROM sites WHERE tenant_id=$1`,
		} {
			if _, err := l.pool.Exec(ctx, q, t); err != nil {
				return err
			}
		}
		for _, q := range []string{
			`INSERT INTO tenants(id,name) VALUES($1,$1) ON CONFLICT DO NOTHING`,
			`INSERT INTO sites(id,tenant_id,name) VALUES($1||'-site',$1,'S') ON CONFLICT DO NOTHING`,
			`INSERT INTO gateways(id,tenant_id,site_id,serial,status) VALUES($1||'-gw',$1,$1||'-site','SER-'||$1,'active') ON CONFLICT DO NOTHING`,
		} {
			if _, err := l.pool.Exec(ctx, q, t); err != nil {
				return err
			}
		}
	}
	return nil
}

func (l pgLoader) Add(ctx context.Context, tenant, device, point string, at time.Time, v float64) error {
	_, err := l.pool.Exec(ctx,
		`INSERT INTO telemetry(event_id,tenant_id,gateway_id,device_id,point_id,observed_at,value,unit,schema_version)
		 VALUES($1,$2,$2||'-gw',$3,$4,$5,$6,'u',1)`,
		tenant+device+point+at.Format(time.RFC3339Nano), tenant, device, point, at, v)
	return err
}

func TestIntegrationTSStoreConformance(t *testing.T) {
	s, _ := testServer(t) // applies migrations
	storetest.Run(t, tsstore.NewPostgres(s.st.Pool), pgLoader{s.st.Pool})
}
