package rules

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Step is one escalation step: once an alert has been open and unacknowledged for
// AfterMinutes, also notify ChannelID. Severity "" matches any severity.
type Step struct {
	Severity     string `json:"severity"`
	Step         int    `json:"step"`
	AfterMinutes int    `json:"after_minutes"`
	ChannelID    string `json:"channel_id,omitempty"`
	// ScheduleID names an on-call schedule instead of one fixed channel: whoever is on duty when the
	// step fires gets it. Exactly one of ChannelID and ScheduleID is set.
	ScheduleID string `json:"schedule_id,omitempty"`
}

// Repeat resends the last step of an alert's chain while the alert stays open and
// unacknowledged. EveryMinutes 0 means off. Max caps the resends per alert (0-10).
type Repeat struct {
	EveryMinutes int `json:"every_minutes"`
	Max          int `json:"max"`
}

// Quiet is a daily window in which reminders (the repeat feature) for non-critical alerts
// are held back. Start and End are "HH:MM" in the IANA zone TZ; a window that ends before it
// starts runs overnight (22:00 to 06:00). Empty Start and End mean off. Escalation steps and
// critical alerts are never held back.
type Quiet struct {
	Start    string `json:"start"`
	End      string `json:"end"`
	Timezone string `json:"timezone"`
}

func parseHM(s string) (int, bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, false
	}
	h, m := int(s[0]-'0')*10+int(s[1]-'0'), int(s[3]-'0')*10+int(s[4]-'0')
	for _, c := range []byte{s[0], s[1], s[3], s[4]} {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	if h > 23 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// ValidateQuiet checks the quiet window before it is saved.
func ValidateQuiet(q Quiet) error {
	if q.Start == "" && q.End == "" {
		if q.Timezone != "" {
			return fmt.Errorf("timezone without a window")
		}
		return nil
	}
	a, ok1 := parseHM(q.Start)
	b, ok2 := parseHM(q.End)
	if !ok1 || !ok2 {
		return fmt.Errorf("quiet start and end must be HH:MM (24 hour)")
	}
	if a == b {
		return fmt.Errorf("quiet start and end must differ")
	}
	if q.Timezone == "" {
		return fmt.Errorf("quiet hours need a timezone such as Asia/Kolkata")
	}
	if _, err := time.LoadLocation(q.Timezone); err != nil {
		return fmt.Errorf("unknown timezone %q", q.Timezone)
	}
	return nil
}

// InQuiet reports whether now falls inside the quiet window.
func InQuiet(now time.Time, q Quiet) bool {
	if q.Start == "" || q.End == "" {
		return false
	}
	a, ok1 := parseHM(q.Start)
	b, ok2 := parseHM(q.End)
	loc, err := time.LoadLocation(q.Timezone)
	if !ok1 || !ok2 || err != nil || a == b {
		return false
	}
	l := now.In(loc)
	m := l.Hour()*60 + l.Minute()
	if a < b {
		return m >= a && m < b
	}
	return m >= a || m < b
}

// ValidateRepeat checks the repeat setting before it is saved.
func ValidateRepeat(r Repeat) error {
	if r.EveryMinutes == 0 {
		if r.Max != 0 {
			return fmt.Errorf("max must be 0 when repeat is off")
		}
		return nil
	}
	if r.EveryMinutes < 5 || r.EveryMinutes > 1440 {
		return fmt.Errorf("repeat every_minutes must be 0 (off) or 5-1440")
	}
	if r.Max < 1 || r.Max > 10 {
		return fmt.Errorf("repeat max must be 1-10")
	}
	return nil
}

// RepeatDue returns the last step of the alert's chain when it should be resent: the
// whole chain has been sent (level is the last step number), fewer than Max resends
// went out, and at least EveryMinutes have passed since the last send.
func RepeatDue(steps []Step, severity string, level, repeats int, sinceLastMinutes float64, r Repeat) (Step, bool) {
	if r.EveryMinutes <= 0 || repeats >= r.Max || level < 1 {
		return Step{}, false
	}
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
	var last Step
	for _, s := range use {
		if s.Step > last.Step {
			last = s
		}
	}
	if last.Step == 0 || level != last.Step || sinceLastMinutes < float64(r.EveryMinutes) {
		return Step{}, false
	}
	return last, true
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
		if (s.ChannelID == "") == (s.ScheduleID == "") {
			return fmt.Errorf("every step needs a channel or an on-call schedule, not both")
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
		`SELECT id, tenant_id, severity, message, escalation_level, EXTRACT(EPOCH FROM now()-created_at)/60,
		        escalation_repeats, COALESCE(EXTRACT(EPOCH FROM now()-escalated_at)/60, 0)
		 FROM alerts WHERE status='open' AND NOT shelved AND created_at > now() - interval '30 days'
		   AND tenant_id IN (SELECT DISTINCT tenant_id FROM escalation_steps) LIMIT 500`)
	if err != nil {
		log.Printf("escalation: %v", err)
		return
	}
	type al struct {
		id, tenant, sev, msg string
		level, repeats       int
		age, since           float64
	}
	var open []al
	for rows.Next() {
		var a al
		if rows.Scan(&a.id, &a.tenant, &a.sev, &a.msg, &a.level, &a.age, &a.repeats, &a.since) == nil {
			open = append(open, a)
		}
	}
	rows.Close()
	cache := map[string][]Step{}
	reps := map[string]Repeat{}
	quiets := map[string]Quiet{}
	for _, a := range open {
		steps, ok := cache[a.tenant]
		if !ok {
			sr, err := pool.Query(ctx, `SELECT severity, step, after_minutes, COALESCE(channel_id,''), COALESCE(schedule_id,'') FROM escalation_steps WHERE tenant_id=$1`, a.tenant)
			if err != nil {
				continue
			}
			for sr.Next() {
				var s Step
				if sr.Scan(&s.Severity, &s.Step, &s.AfterMinutes, &s.ChannelID, &s.ScheduleID) == nil {
					steps = append(steps, s)
				}
			}
			sr.Close()
			cache[a.tenant] = steps
			var r Repeat
			if pool.QueryRow(ctx, `SELECT repeat_every_minutes, repeat_max FROM escalation_settings WHERE tenant_id=$1`, a.tenant).Scan(&r.EveryMinutes, &r.Max) == nil {
				reps[a.tenant] = r
			}
			var q Quiet
			if pool.QueryRow(ctx, `SELECT quiet_start, quiet_end, quiet_tz FROM escalation_settings WHERE tenant_id=$1`, a.tenant).Scan(&q.Start, &q.End, &q.Timezone) == nil {
				quiets[a.tenant] = q
			}
		}
		st, due := NextStep(steps, a.sev, a.level, a.age)
		repeat := false
		if !due {
			st, due = RepeatDue(steps, a.sev, a.level, a.repeats, a.since, reps[a.tenant])
			if !due {
				continue
			}
			// quiet hours hold back reminders for non-critical alerts; the alert stays open, so
			// the reminder goes out once the window ends
			if a.sev != "critical" && InQuiet(time.Now(), quiets[a.tenant]) {
				continue
			}
			repeat = true
		}
		// claim the step first (only if nobody acknowledged or escalated meanwhile)
		q, args := `UPDATE alerts SET escalation_level=$1, escalated_at=now() WHERE id=$2 AND status='open' AND escalation_level=$3`, []any{st.Step, a.id, a.level}
		if repeat {
			q, args = `UPDATE alerts SET escalation_repeats=escalation_repeats+1, escalated_at=now() WHERE id=$1 AND status='open' AND escalation_level=$2 AND escalation_repeats=$3`, []any{a.id, a.level, a.repeats}
		}
		tag, err := pool.Exec(ctx, q, args...)
		if err != nil || tag.RowsAffected() == 0 {
			continue
		}
		chID := st.ChannelID
		if st.ScheduleID != "" {
			var sc Schedule
			if pool.QueryRow(ctx, `SELECT anchor, shift_hours, channel_ids FROM oncall_schedules WHERE id=$1 AND tenant_id=$2`, st.ScheduleID, a.tenant).
				Scan(&sc.Anchor, &sc.ShiftHours, &sc.ChannelIDs) != nil {
				log.Printf("escalation: alert %s step %d: on-call schedule %s missing", a.id, st.Step, st.ScheduleID)
				continue
			}
			chID, _, _ = OnCall(sc, time.Now())
		}
		var typ, target string
		var enabled bool
		if pool.QueryRow(ctx, `SELECT type, target, enabled FROM notification_channels WHERE id=$1 AND tenant_id=$2`, chID, a.tenant).Scan(&typ, &target, &enabled) != nil || !enabled {
			log.Printf("escalation: alert %s step %d: channel %s missing or disabled", a.id, st.Step, chID)
			continue
		}
		msg := fmt.Sprintf("ESCALATION step %d (unacknowledged for %d min): %s", st.Step, int(a.age), a.msg)
		if repeat {
			msg = fmt.Sprintf("ESCALATION REMINDER %d of %d, step %d (unacknowledged for %d min): %s", a.repeats+1, reps[a.tenant].Max, st.Step, int(a.age), a.msg)
		}
		dispatchOne(ctx, n, typ, target, a.sev, msg, "alert.escalated")
	}
}
