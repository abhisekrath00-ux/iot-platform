package flow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/control"
)

// ControlRequester raises a control REQUEST for an allowlisted target. It never actuates: the
// request lands as pending_approval and a person other than the requester must approve it.
type ControlRequester interface {
	RequestControl(ctx context.Context, targetID string, value float64) (requestID, note string, err error)
}

const (
	maxControlPerRun    = 1
	maxPendingPerFlow   = 3
	ControlFeature      = "control_nodes"
	controlSystemUserFm = "flow-engine:%s"
)

// PGControl is the production ControlRequester. Dry runs and the simulator never get one.
type PGControl struct {
	Pool     *pgxpool.Pool
	Tenant   string
	FlowID   string
	FlowName string
	// What started the run, shown to the approver.
	TrigDevice, TrigPoint string
	TrigValue             float64
}

func (c *PGControl) RequestControl(ctx context.Context, targetID string, value float64) (string, string, error) {
	if !tenantFeature(ctx, c.Pool, c.Tenant, ControlFeature) {
		return "", "", errors.New("control nodes are not enabled for this tenant")
	}
	var t control.Target
	err := c.Pool.QueryRow(ctx, `SELECT id,name,kind,gateway_id,device_id,point_id,min_value,max_value,allowed_values,max_per_hour,approval_mode,enabled
	    FROM control_targets WHERE id=$1 AND tenant_id=$2`, targetID, c.Tenant).
		Scan(&t.ID, &t.Name, &t.Kind, &t.GatewayID, &t.DeviceID, &t.PointID, &t.Min, &t.Max, &t.AllowedValues, &t.MaxPerHour, &t.ApprovalMode, &t.Enabled)
	if err != nil {
		return "", "", errors.New("control target not found")
	}
	if !t.Enabled {
		return "", "", fmt.Errorf("control target %q is switched off", t.Name)
	}
	if err := t.CheckValue(value); err != nil {
		return "", "", fmt.Errorf("refused for %q: %v", t.Name, err)
	}
	var recent, pending int
	c.Pool.QueryRow(ctx, `SELECT count(*) FROM commands WHERE control_target_id=$1 AND created_at > now() - interval '1 hour'`, t.ID).Scan(&recent)
	if recent >= t.MaxPerHour {
		return "", "", fmt.Errorf("rate limit for %q: %d requests in the last hour", t.Name, recent)
	}
	c.Pool.QueryRow(ctx, `SELECT count(*) FROM commands WHERE source_flow_id=$1 AND status='pending_approval'`, c.FlowID).Scan(&pending)
	if pending >= maxPendingPerFlow {
		return "", "", fmt.Errorf("this flow already has %d requests waiting for approval", pending)
	}
	// The requester is a per-tenant service user with the viewer role: it can never approve, and the
	// four-eyes rule (requester differs from approver) holds without a special case.
	uid := fmt.Sprintf(controlSystemUserFm, c.Tenant)
	if _, err := c.Pool.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name,role) VALUES($1,$2,$3,'Flow engine','viewer') ON CONFLICT DO NOTHING`,
		uid, c.Tenant, uid+"@system.invalid"); err != nil {
		return "", "", errors.New("could not record the requester")
	}
	params, _ := json.Marshal(map[string]any{"point": t.PointID, "value": value})
	reason := fmt.Sprintf("flow %q, reading %s/%s = %v", c.FlowName, c.TrigDevice, c.TrigPoint, c.TrigValue)
	id := uuid.NewString()
	if _, err := c.Pool.Exec(ctx, `INSERT INTO commands(request_id,tenant_id,gateway_id,device_id,action,parameters,requested_by,source_flow_id,control_target_id,reason)
	    VALUES($1,$2,$3,$4,'modbus.write',$5,$6,$7,$8,$9)`, id, c.Tenant, t.GatewayID, t.DeviceID, params, uid, c.FlowID, t.ID, reason); err != nil {
		return "", "", errors.New("could not create the request")
	}
	detail, _ := json.Marshal(map[string]any{"flow": c.FlowName, "target": t.Name, "device": t.DeviceID, "point": t.PointID, "value": value,
		"trigger": reason, "target_mode": t.ApprovalMode})
	c.Pool.Exec(ctx, `INSERT INTO audit_log(tenant_id,actor,action,target,detail) VALUES($1,$2,'control.request',$3,$4)`, c.Tenant, "flow:"+c.FlowID, id, detail)
	note := ""
	if t.ApprovalMode == "automatic" {
		// Fail toward the stricter path: the automatic executor is not built, so a person still approves.
		note = "target is set to automatic, but automatic execution is not built yet; a person must approve this request"
	}
	return id, note, nil
}
