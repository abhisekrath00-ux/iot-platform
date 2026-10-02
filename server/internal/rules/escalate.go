package rules

import (
	"context"
	"fmt"
	"log"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Step is one escalation step: once an alert has been open and unacknowledged for
// AfterMinutes, also notify ChannelID. Severity "" matches any severity.
type Step struct {
	Severity     string `json:"severity"`
	Step         int    `json:"step"`
	AfterMinutes int    `json:"after_minutes"`
	ChannelID    string `json:"channel_id"`
}

// NextStep picks the next step due for an alert, or false. level is the highest
// step already sent for this alert. Steps for the alert's own severity win over
// "any" steps (a tenant that defines critical steps does not also get the generic ones
// on critical alerts). Only one step is returned per call, so a long outage
// escalates one step per sweep, in order, never skipping.
func NextStep(steps []Step, severity string, level int, ageMinutes float64) (Step, bool) {
	var own, any []Step
	for _, s := range steps {
		switch s.Severity {
		case severity:
			own = append(own, s)
		case "":
			any = append(any, s)
		}
	}
	use := any
	if len(own) > 0 {
		use = own
	}
	sort.Slice(use, func(i, j int) bool { return use[i].Step < use[j].Step })
	for _, s := range use {
		if s.Step > level {
			if ageMinutes >= float64(s.AfterMinutes) {
				return s, true
			}
			return Step{}, false
		}
	}
	return Step{}, false
}

// ValidateSteps checks a tenant's escalation policy before it is saved.
func ValidateSteps(steps []Step) error {
	if len(steps) > 15 {
		return fmt.Errorf("at most 15 steps")
	}
	byGroup := map[string][]Step{}
	for _, s := range steps {
		switch s.Severity {
		case "", "info", "warning", "critical":
		default:
			return fmt.Errorf("severity must be empty (any), info, warning or critical")
		}
		if s.ChannelID == "" {
			return fmt.Errorf("every step needs a channel")
		}
		if s.AfterMinutes < 1 || s.AfterMinutes > 10080 {
			return fmt.Errorf("after_minutes must be 1-10080")
		}
		byGroup[s.Severity] = append(byGroup[s.Severity], s)
	}
	for sev, g := range byGroup {
		if len(g) > 5 {
			return fmt.Errorf("at most 5 steps per severity")
		}
		sort.Slice(g, func(i, j int) bool { return g[i].Step < g[j].Step })
		for i, s := range g {
			if s.Step != i+1 {
				return fmt.Errorf("steps for %q must be numbered 1, 2, 3 without gaps", sev)
			}
			if i > 0 && s.AfterMinutes <= g[i-1].AfterMinutes {
				return fmt.Errorf("after_minutes must grow with each step (severity %q)", sev)
			}
		}
	}
	return nil
}

// EvaluateEscalations notifies the next due step of every open, unacknowledged
// alert. Run it about once a minute from one replica. Acknowledging or resolving
// an alert stops it. Each step is recorded on the alert before it is sent, so a
// crash can lose one notification but never repeat a step.
func EvaluateEscalations(ctx context.Context, pool *pgxpool.Pool, n Notifier) {
	if n == nil {
		return
	}
	rows, err := pool.Query(ctx,
		`SELECT id, tenant_id, severity, message, escalation_level, EXTRACT(EPOCH FROM now()-created_at)/60
		 FROM alerts WHERE status='open' AND created_at > now() - interval '30 days'
		   AND tenant_id IN (SELECT DISTINCT tenant_id FROM escalation_steps) LIMIT 500`)
	if err != nil {
		log.Printf("escalation: %v", err)
		return
	}
	type al struct {
		id, tenant, sev, msg string
		level                int
		age                  float64
	}
	var open []al
	for rows.Next() {
		var a al
		if rows.Scan(&a.id, &a.tenant, &a.sev, &a.msg, &a.level, &a.age) == nil {
			open = append(open, a)
		}
	}
	rows.Close()
	cache := map[string][]Step{}
	for _, a := range open {
		steps, ok := cache[a.tenant]
		if !ok {
			sr, err := pool.Query(ctx, `SELECT severity, step, after_minutes, channel_id FROM escalation_steps WHERE tenant_id=$1`, a.tenant)
			if err != nil {
				continue
			}
			for sr.Next() {
				var s Step
				if sr.Scan(&s.Severity, &s.Step, &s.AfterMinutes, &s.ChannelID) == nil {
					steps = append(steps, s)
				}
			}
			sr.Close()
			cache[a.tenant] = steps
		}
		st, due := NextStep(steps, a.sev, a.level, a.age)
		if !due {
			continue
		}
		// claim the step first (only if nobody acknowledged or escalated meanwhile)
		tag, err := pool.Exec(ctx,
			`UPDATE alerts SET escalation_level=$1, escalated_at=now() WHERE id=$2 AND status='open' AND escalation_level=$3`, st.Step, a.id, a.level)
		if err != nil || tag.RowsAffected() == 0 {
			continue
		}
		var typ, target string
		var enabled bool
		if pool.QueryRow(ctx, `SELECT type, target, enabled FROM notification_channels WHERE id=$1 AND tenant_id=$2`, st.ChannelID, a.tenant).Scan(&typ, &target, &enabled) != nil || !enabled {
			log.Printf("escalation: alert %s step %d: channel %s missing or disabled", a.id, st.Step, st.ChannelID)
			continue
		}
		msg := fmt.Sprintf("ESCALATION step %d (unacknowledged for %d min): %s", st.Step, int(a.age), a.msg)
		dispatchOne(ctx, n, typ, target, a.sev, msg, "alert.escalated")
	}
}
