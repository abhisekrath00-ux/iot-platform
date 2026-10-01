// Package rules evaluates enabled v1 threshold rules against incoming
// telemetry and raises alerts with notification dispatch.
package rules

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Definition struct {
	DeviceID string `json:"device_id"` // empty = any device exposing the point
	// Profile limits the rule to devices of one device profile (asset type), so
	// one rule covers every device of that type, now and in the future.
	Profile   string  `json:"profile,omitempty"`
	PointID   string  `json:"point_id"`
	Op        string  `json:"op"` // ">" or "<"
	Threshold float64 `json:"threshold"`
	Severity  string  `json:"severity"` // info|warning|critical
	Message   string  `json:"message"`
}

type Notifier interface {
	Email(ctx context.Context, to []string, subject, body string) error
	Slack(ctx context.Context, channel, text string) error
}

// Evaluate checks tenant rules against one reading and raises alerts.
func Evaluate(ctx context.Context, pool *pgxpool.Pool, n Notifier, tenantID, deviceID, pointID string, value float64) {
	rows, err := pool.Query(ctx,
		`SELECT id, definition FROM rules WHERE tenant_id=$1 AND enabled`, tenantID)
	if err != nil {
		log.Printf("rules: load: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var raw json.RawMessage
		if err := rows.Scan(&id, &raw); err != nil {
			continue
		}
		var d Definition
		if err := json.Unmarshal(raw, &d); err != nil {
			continue
		}
		if d.PointID != pointID || (d.DeviceID != "" && d.DeviceID != deviceID) {
			continue
		}
		if d.Profile != "" {
			var prof string
			if pool.QueryRow(ctx, `SELECT profile FROM devices WHERE id=$1 AND tenant_id=$2`, deviceID, tenantID).Scan(&prof) != nil || prof != d.Profile {
				continue
			}
		}
		hit := (d.Op == ">" && value > d.Threshold) || (d.Op == "<" && value < d.Threshold)
		if !hit {
			continue
		}
		fire(ctx, pool, n, tenantID, id, d, deviceID, pointID, value)
	}
}

func fire(ctx context.Context, pool *pgxpool.Pool, n Notifier, tenantID, ruleID string, d Definition, deviceID, pointID string, value float64) {
	// one open alert per rule and device: dedupe
	var existing string
	err := pool.QueryRow(ctx,
		`SELECT id FROM alerts WHERE rule_id=$1 AND status='open' AND COALESCE(device_id,'')=$2 LIMIT 1`, ruleID, deviceID).Scan(&existing)
	if err == nil {
		return // already open
	}
	msg := d.Message
	if msg == "" {
		msg = fmt.Sprintf("%s/%s %s %v (value %.3g)", deviceID, pointID, d.Op, d.Threshold, value)
	}
	alertID := uuid.NewString()
	if _, err := pool.Exec(ctx,
		`INSERT INTO alerts(id,tenant_id,rule_id,severity,message,device_id) VALUES($1,$2,$3,$4,$5,$6)`,
		alertID, tenantID, ruleID, d.Severity, msg, deviceID); err != nil {
		log.Printf("rules: insert alert: %v", err)
		return
	}
	dispatch(ctx, pool, n, tenantID, d.Severity, msg)
}

func dispatch(ctx context.Context, pool *pgxpool.Pool, n Notifier, tenantID, severity, msg string) {
	if n == nil {
		return
	}
	rows, err := pool.Query(ctx,
		`SELECT type, target FROM notification_channels WHERE tenant_id=$1 AND enabled`, tenantID)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var typ, target string
		if err := rows.Scan(&typ, &target); err != nil {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		var err error
		switch typ {
		case "email":
			err = n.Email(cctx, []string{target}, "[Hexmon IoT] "+severity+" alert", msg)
		case "slack":
			err = n.Slack(cctx, target, "["+severity+"] "+msg)
		}
		cancel()
		if err != nil {
			log.Printf("notify %s -> %s: %v", typ, target, err)
		}
	}
}
