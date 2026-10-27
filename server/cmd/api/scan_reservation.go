package main

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
)

var (
	errScanGatewayMissing  = errors.New("scan gateway missing")
	errScanGatewayInactive = errors.New("scan gateway inactive")
	errScanAlreadyRunning  = errors.New("scan already running")
)

// Serialize manual reservations on the existing tenant-owned gateway row.
// Commit before publishing, so another tab/replica sees the reservation even
// while the broker call is pending. This is not an exactly-once publish gate.
func (s *server) reserveManualScan(ctx context.Context, tenant, gw, id, kind string, params []byte, user string) error {
	tx, err := s.st.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM gateways WHERE id=$1 AND tenant_id=$2 FOR NO KEY UPDATE`, gw, tenant).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return errScanGatewayMissing
	}
	if err != nil {
		return err
	}
	if status != "active" {
		return errScanGatewayInactive
	}
	var running bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM gateway_scans WHERE gateway_id=$1 AND tenant_id=$2 AND status='requested' AND created_at > now()-interval '10 minutes')`, gw, tenant).Scan(&running); err != nil {
		return err
	}
	if running {
		return errScanAlreadyRunning
	}
	_, err = tx.Exec(ctx, `INSERT INTO gateway_scans(id,tenant_id,gateway_id,kind,params,requested_by) VALUES($1,$2,$3,$4,$5,$6)`, id, tenant, gw, kind, params, user)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
