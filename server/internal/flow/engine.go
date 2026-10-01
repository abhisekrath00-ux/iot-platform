package flow

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Notifier interface {
	Email(ctx context.Context, to []string, subject, body string) error
	Slack(ctx context.Context, channel, text string) error
}

// Evaluate runs enabled flows whose trigger matches the incoming reading.
// Only the PUBLISHED version of each flow executes; drafts never fire.
// Delays execute in a goroutine so ingest never blocks; each dispatch is
// recorded in flow_runs.
func Evaluate(ctx context.Context, pool *pgxpool.Pool, n Notifier, tenantID, deviceID, pointID string, value float64) {
	rows, err := pool.Query(ctx,
		`SELECT f.id, f.name, v.definition
		 FROM flows f JOIN flow_versions v ON v.id = f.published_version_id
		 WHERE f.tenant_id=$1 AND f.enabled`, tenantID)
	if err != nil {
		log.Printf("flows: load: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		var defBytes []byte
		if rows.Scan(&id, &name, &defBytes) != nil {
			continue
		}
		var d Definition
		if json.Unmarshal(defBytes, &d) != nil {
			continue
		}
		if d.Trigger.DeviceID != deviceID || d.Trigger.PointID != pointID {
			continue
		}
		actions, wait, ok := Run(d, value)
		outcome := "notified"
		if ok && len(actions) > 0 && d.CooldownSeconds > 0 {
			var last time.Time
			err := pool.QueryRow(ctx,
				`SELECT created_at FROM flow_runs WHERE flow_id=$1 AND outcome='notified' ORDER BY created_at DESC LIMIT 1`, id).Scan(&last)
			if err == nil && InCooldown(last, time.Now(), d.CooldownSeconds) {
				pool.Exec(ctx, `INSERT INTO flow_runs(id,flow_id,trigger_value,outcome) VALUES($1,$2,$3,'suppressed_cooldown')`,
					uuid.NewString(), id, value)
				continue
			}
		}
		if !ok {
			outcome = "skipped_condition"
		}
		if len(actions) == 0 && ok {
			outcome = "skipped_condition"
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO flow_runs(id,flow_id,trigger_value,outcome) VALUES($1,$2,$3,$4)`,
			uuid.NewString(), id, value, outcome); err != nil {
			log.Printf("flows: record run: %v", err)
		}
		if !ok || len(actions) == 0 {
			continue
		}
		go func(flowID, flowName string, acts []Action, w time.Duration) {
			bg := context.Background()
			if w > 0 {
				time.Sleep(w) // validated max 1h
			}
			for _, a := range acts {
				var ctype, target string
				var enabled bool
				err := pool.QueryRow(bg,
					`SELECT type, target, enabled FROM notification_channels WHERE id=$1 AND tenant_id=$2`,
					a.ChannelID, tenantID).Scan(&ctype, &target, &enabled)
				if err != nil || !enabled {
					log.Printf("flow %s: channel %s unavailable", flowID, a.ChannelID)
					continue
				}
				var derr error
				if ctype == "email" {
					derr = n.Email(bg, []string{target}, "Flow: "+flowName, a.Message)
				} else {
					derr = n.Slack(bg, target, "["+flowName+"] "+a.Message)
				}
				if derr != nil {
					log.Printf("flow %s: dispatch: %v", flowID, derr)
				}
			}
		}(id, name, actions, wait)
	}
}
