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
var sharedLimiter = &MemLimiter{}

func Evaluate(ctx context.Context, pool *pgxpool.Pool, n Notifier, tenantID, deviceID, pointID string, value float64) {
	EvaluateWith(ctx, pool, n, nil, tenantID, deviceID, pointID, value)
}

// EvaluateWith is Evaluate with a function-node runner. Function nodes only run
// when fn is non-nil AND the tenant has enabled them (tenant_features).
func EvaluateWith(ctx context.Context, pool *pgxpool.Pool, n Notifier, fn FunctionRunner, tenantID, deviceID, pointID string, value float64) {
	fnOn := -1 // lazily resolved: -1 unknown, 0 off, 1 on
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
		if t := d.Trig(); t.DeviceID != deviceID || t.PointID != pointID {
			continue
		}
		opt := ExecOptions{Limiter: sharedLimiter, LimitKey: id}
		if fn != nil && d.HasFunctionNodes() {
			if fnOn < 0 {
				fnOn = 0
				var on bool
				if pool.QueryRow(ctx, `SELECT enabled FROM tenant_features WHERE tenant_id=$1 AND feature='function_nodes'`, tenantID).Scan(&on) == nil && on {
					fnOn = 1
				}
			}
			if fnOn == 1 {
				opt.Functions = fn
			}
		}
		if HTTPClient != nil && d.HasHTTPNodes() && tenantFeature(ctx, pool, tenantID, "http_nodes") {
			opt.HTTP = HTTPClient
		}
		if d.HasControlNodes() && tenantFeature(ctx, pool, tenantID, ControlFeature) {
			opt.Control = &PGControl{Pool: pool, Tenant: tenantID, FlowID: id, FlowName: name, TrigDevice: deviceID, TrigPoint: pointID, TrigValue: value}
		}
		if d.HasContextNodes() {
			opt.Context = &PGContext{Pool: pool, Tenant: tenantID}
		}
		er := d.Exec(value, deviceID, pointID, opt)
		actions, ok := er.Actions, er.Matched
		detail := debugDetail(er.Debug)
		outcome := "notified"
		if ok && len(actions) > 0 && d.Latch {
			var last string
			if pool.QueryRow(ctx, `SELECT outcome FROM flow_runs WHERE flow_id=$1 ORDER BY created_at DESC, id DESC LIMIT 1`, id).Scan(&last) == nil && LatchHolds(true, last) {
				pool.Exec(ctx, `INSERT INTO flow_runs(id,flow_id,trigger_value,outcome) VALUES($1,$2,$3,'suppressed_latch')`,
					uuid.NewString(), id, value)
				continue
			}
		}
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
			`INSERT INTO flow_runs(id,flow_id,trigger_value,outcome,detail) VALUES($1,$2,$3,$4,NULLIF($5,''))`,
			uuid.NewString(), id, value, outcome, detail); err != nil {
			log.Printf("flows: record run: %v", err)
		}
		if !ok || len(actions) == 0 {
			continue
		}
		go dispatch(pool, n, tenantID, id, name, deviceID, pointID, value, actions)
	}
}

// debugDetail renders debug-node output for flow_runs.detail (bounded).
func debugDetail(es []DebugEntry) string {
	out := ""
	for _, e := range es {
		out += e.Node + ": " + e.Message + "\n"
		if len(out) > 2000 {
			return out[:2000]
		}
	}
	return out
}

// dispatch sends the actions of one run: it waits out each action's delay, then
// resolves the tenant's channel and delivers. Runs in its own goroutine.
func dispatch(pool *pgxpool.Pool, n Notifier, tenantID, flowID, flowName, deviceID, pointID string, value float64, acts []Action) {
	bg := context.Background()
	for _, a := range acts {
		if a.Delay > 0 {
			time.Sleep(a.Delay) // capped at 1h per path
		}
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
		switch ctype {
		case "email":
			derr = n.Email(bg, []string{target}, "Flow: "+flowName, a.Message)
		case "slack":
			derr = n.Slack(bg, target, "["+flowName+"] "+a.Message)
		case "teams":
			if t, ok := n.(interface {
				Teams(context.Context, string, string, string) error
			}); ok {
				derr = t.Teams(bg, target, "Flow: "+flowName, a.Message)
			}
		case "sms":
			if t, ok := n.(interface {
				SMS(context.Context, string, string) error
			}); ok {
				derr = t.SMS(bg, target, "["+flowName+"] "+a.Message)
			}
		case "webhook":
			if wh, ok := n.(interface {
				Webhook(context.Context, string, string, map[string]any) error
			}); ok {
				derr = wh.Webhook(bg, target, "flow.notify", map[string]any{"flow": flowName, "message": a.Message, "device_id": deviceID, "point_id": pointID, "value": value})
			}
		}
		if derr != nil {
			log.Printf("flow %s: dispatch: %v", flowID, derr)
		}
	}
}

// RunScheduled starts every published, enabled flow whose inject node is due.
// Call it periodically from ONE replica (leader election): due-ness is read
// from flow_runs, so a restart or a leader change does not double-fire within
// an interval. Function nodes do not run in scheduled flows.
func RunScheduled(ctx context.Context, pool *pgxpool.Pool, n Notifier) {
	rows, err := pool.Query(ctx,
		`SELECT f.id, f.tenant_id, f.name, v.definition,
		        (SELECT max(created_at) FROM flow_runs r WHERE r.flow_id=f.id)
		 FROM flows f JOIN flow_versions v ON v.id = f.published_version_id
		 WHERE f.enabled AND v.definition::text LIKE '%"inject"%'`)
	if err != nil {
		log.Printf("flows: scheduled load: %v", err)
		return
	}
	type due struct {
		id, tenant, name string
		d                Definition
	}
	var list []due
	now := time.Now()
	for rows.Next() {
		var id, tenant, name string
		var defBytes []byte
		var last *time.Time
		if rows.Scan(&id, &tenant, &name, &defBytes, &last) != nil {
			continue
		}
		var d Definition
		if json.Unmarshal(defBytes, &d) != nil || d.Graph == nil {
			continue
		}
		var inj *Node
		for i := range d.Graph.Nodes {
			if d.Graph.Nodes[i].Type == "inject" {
				inj = &d.Graph.Nodes[i]
			}
		}
		if inj == nil || (last != nil && now.Sub(*last) < time.Duration(inj.Seconds)*time.Second) {
			continue
		}
		list = append(list, due{id, tenant, name, d})
	}
	rows.Close()
	for _, f := range list {
		sopt := ExecOptions{Scheduled: true, Limiter: sharedLimiter, LimitKey: f.id}
		if HTTPClient != nil && f.d.HasHTTPNodes() && tenantFeature(ctx, pool, f.tenant, "http_nodes") {
			sopt.HTTP = HTTPClient
		}
		if f.d.HasControlNodes() && tenantFeature(ctx, pool, f.tenant, ControlFeature) {
			sopt.Control = &PGControl{Pool: pool, Tenant: f.tenant, FlowID: f.id, FlowName: f.name, TrigDevice: "", TrigPoint: "scheduled", TrigValue: 0}
		}
		if f.d.HasContextNodes() {
			sopt.Context = &PGContext{Pool: pool, Tenant: f.tenant}
		}
		er := f.d.Exec(0, "", "", sopt)
		outcome := "notified"
		if len(er.Actions) == 0 {
			outcome = "skipped_condition"
		}
		inj := f.d.Graph.Nodes
		var val float64
		for _, nd := range inj {
			if nd.Type == "inject" {
				val = nd.Value
			}
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO flow_runs(id,flow_id,trigger_value,outcome,detail) VALUES($1,$2,$3,$4,NULLIF($5,''))`,
			uuid.NewString(), f.id, val, outcome, debugDetail(er.Debug)); err != nil {
			log.Printf("flows: record scheduled run: %v", err)
			continue
		}
		if len(er.Actions) > 0 {
			var did, pid string
			for _, nd := range inj {
				if nd.Type == "inject" {
					did, pid = nd.DeviceID, nd.PointID
				}
			}
			go dispatch(pool, n, f.tenant, f.id, f.name, did, pid, val, er.Actions)
		}
	}
}

// HTTPClient performs the outbound requests of http nodes. The API and ingest
// services set it at startup (notify.Notifier); nil disables http nodes everywhere.
var HTTPClient HTTPDoer

func tenantFeature(ctx context.Context, pool *pgxpool.Pool, tenant, feature string) bool {
	var on bool
	return pool.QueryRow(ctx, `SELECT enabled FROM tenant_features WHERE tenant_id=$1 AND feature=$2`, tenant, feature).Scan(&on) == nil && on
}
