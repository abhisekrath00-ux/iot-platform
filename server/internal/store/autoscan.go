package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	autoScanKeep     = 20 // newest auto results kept per gateway
	autoScanPerHour  = 30 // hard cap on how many one gateway may add per hour
	autoScanMaxBytes = 512 * 1024
)

// ErrAutoScan is returned for results that are refused (bad shape, unknown gateway, rate limit).
var ErrAutoScan = errors.New("auto scan result refused")

// StoreAutoScan saves a result the edge produced on its own (scan_id "auto-...").
// It is only ever a proposal: it creates a finished scan row that the operator can
// add devices from, with the usual checks in the add endpoint. Tenant and gateway
// come from the MQTT topic (not the payload), the gateway must exist for that
// tenant, the kind is allowlisted, and volume per gateway is capped.
func (s *Store) StoreAutoScan(ctx context.Context, tenant, gw string, payload []byte) error {
	if len(payload) > autoScanMaxBytes {
		return fmt.Errorf("%w: too large", ErrAutoScan)
	}
	var p struct {
		ScanID string         `json:"scan_id"`
		Kind   string         `json:"kind"`
		OK     bool           `json:"ok"`
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal(payload, &p); err != nil || !strings.HasPrefix(p.ScanID, "auto-") || len(p.ScanID) > 64 || !p.OK {
		return fmt.Errorf("%w: bad payload", ErrAutoScan)
	}
	switch p.Kind {
	case "modbus-rtu", "lan", "bacnet":
	default:
		return fmt.Errorf("%w: unknown kind", ErrAutoScan)
	}
	if p.Params == nil {
		p.Params = map[string]any{}
	}
	p.Params["auto"] = true
	params, _ := json.Marshal(p.Params)

	var recent int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM gateway_scans WHERE tenant_id=$1 AND gateway_id=$2 AND requested_by='edge-autodetect' AND created_at > now() - interval '1 hour'`, tenant, gw).Scan(&recent); err != nil {
		return err
	}
	if recent >= autoScanPerHour {
		return fmt.Errorf("%w: rate limit", ErrAutoScan)
	}
	tag, err := s.Pool.Exec(ctx,
		`INSERT INTO gateway_scans(id, tenant_id, gateway_id, kind, params, status, result, requested_by)
		 SELECT $1, g.tenant_id, g.id, $2, $3::jsonb, 'done', $4::jsonb, 'edge-autodetect'
		 FROM gateways g WHERE g.id=$5 AND g.tenant_id=$6
		 ON CONFLICT (id) DO NOTHING`, p.ScanID, p.Kind, string(params), string(payload), gw, tenant)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: unknown gateway or duplicate", ErrAutoScan)
	}
	_, err = s.Pool.Exec(ctx,
		`DELETE FROM gateway_scans WHERE tenant_id=$1 AND gateway_id=$2 AND requested_by='edge-autodetect'
		   AND id NOT IN (SELECT id FROM gateway_scans WHERE tenant_id=$1 AND gateway_id=$2 AND requested_by='edge-autodetect' ORDER BY created_at DESC LIMIT $3)`,
		tenant, gw, autoScanKeep)
	return err
}
