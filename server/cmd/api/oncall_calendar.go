package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/googlews"
)

// Google Calendar on-call sync (optional surface; calendar.readonly scope).
// A schedule linked to a calendar derives its current on-call channel from
// the event spanning now: the event summary must CONTAIN the notification
// channel's target (for example the on-call person's email address). No
// spanning event, no matching channel, a failed sync or a stale result all
// fall back to the static rotation - the calendar can only ever sharpen,
// never break, escalation. Sync writes nothing to Google.

// calStaleBound is how long a successful sync result stays authoritative.
const calStaleBound = 15 * time.Minute

// PUT /v1/oncall/{id}/calendar (admin): body {"calendar_id": "..."} links a
// Google calendar; an empty id unlinks. Linking validates the calendar is
// readable right now through the tenant's grant, then runs one sync so the
// response shows the derived state immediately.
func (s *server) putOnCallCalendar(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	id, tenant := r.PathValue("id"), auth.Tenant(r)
	var exists int
	if err := s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM oncall_schedules WHERE id=$1 AND tenant_id=$2`, id, tenant).Scan(&exists); err != nil || exists == 0 {
		http.Error(w, "schedule not found", 404)
		return
	}
	var in struct {
		CalendarID string `json:"calendar_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	in.CalendarID = strings.TrimSpace(in.CalendarID)
	if in.CalendarID == "" {
		if _, err := s.st.Pool.Exec(r.Context(), `UPDATE oncall_schedules SET google_calendar_id='', cal_channel_id=NULL, cal_until=NULL, cal_summary='', cal_synced_at=NULL, cal_sync_error='' WHERE id=$1 AND tenant_id=$2`, id, tenant); err != nil {
			dbErr(w, err)
			return
		}
		s.audit(r, "oncall.calendar_unlinked", id, nil)
		writeJSON(w, 200, map[string]any{"google_calendar_id": ""})
		return
	}
	if s.google == nil {
		writeJSON(w, 503, map[string]any{"error": "google workspace not configured on this deployment (GOOGLE_CLIENT_ID/SECRET)"})
		return
	}
	if _, err := googlews.AccessToken(r.Context(), s.google, s.secrets, tenant); err != nil {
		http.Error(w, "connect Google Workspace for this workspace first (Settings > Integrations > Google)", 409)
		return
	}
	if err := googlews.RequireScope(r.Context(), s.secrets, tenant, googlews.ScopeCalendarOnCall); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	if err := s.syncOneCalendarSchedule(r.Context(), tenant, id, in.CalendarID); err != nil {
		http.Error(w, "calendar is not readable with the connected grant: "+err.Error(), 502)
		return
	}
	if _, err := s.st.Pool.Exec(r.Context(), `UPDATE oncall_schedules SET google_calendar_id=$3 WHERE id=$1 AND tenant_id=$2`, id, tenant, in.CalendarID); err != nil {
		dbErr(w, err)
		return
	}
	s.audit(r, "oncall.calendar_linked", id, map[string]any{"calendar_id": in.CalendarID})
	var calSummary, calErr string
	var calCh sql.NullString
	var calUntil, calSynced sql.NullTime
	s.st.Pool.QueryRow(r.Context(), `SELECT cal_channel_id, cal_until, cal_summary, cal_synced_at, cal_sync_error FROM oncall_schedules WHERE id=$1 AND tenant_id=$2`, id, tenant).
		Scan(&calCh, &calUntil, &calSummary, &calSynced, &calErr)
	writeJSON(w, 200, map[string]any{
		"google_calendar_id": in.CalendarID, "cal_channel_id": calCh.String, "cal_until": calUntil.Time,
		"cal_summary": calSummary, "cal_sync_error": calErr,
	})
}

// calendarOnCallLoop refreshes every linked schedule every 2 minutes. Absent
// Google configuration it idles - the air-gapped default is "feature absent".
func (s *server) calendarOnCallLoop(ctx context.Context) {
	t := time.NewTicker(2 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if s.google != nil && s.secrets != nil && len(s.secrets.Key) > 0 {
				s.syncCalendarSchedules(ctx)
			}
		}
	}
}

// syncCalendarSchedules syncs every linked schedule, minting one access
// token per tenant for the whole sweep.
func (s *server) syncCalendarSchedules(ctx context.Context) {
	rows, err := s.st.Pool.Query(ctx, `SELECT DISTINCT tenant_id FROM oncall_schedules WHERE google_calendar_id <> ''`)
	if err != nil {
		log.Printf("calendar sync: list tenants: %v", err)
		return
	}
	var tenants []string
	for rows.Next() {
		var t string
		if rows.Scan(&t) == nil {
			tenants = append(tenants, t)
		}
	}
	rows.Close()
	for _, tenant := range tenants {
		scoped, cancel := context.WithTimeout(ctx, 30*time.Second)
		rows2, err := s.st.Pool.Query(scoped, `SELECT id, google_calendar_id FROM oncall_schedules WHERE tenant_id=$1 AND google_calendar_id <> ''`, tenant)
		if err != nil {
			cancel()
			continue
		}
		type link struct{ id, cal string }
		var links []link
		for rows2.Next() {
			var l link
			if rows2.Scan(&l.id, &l.cal) == nil {
				links = append(links, l)
			}
		}
		rows2.Close()
		for _, l := range links {
			if err := s.syncOneCalendarSchedule(scoped, tenant, l.id, l.cal); err != nil {
				log.Printf("calendar sync: tenant %s schedule %s: %v", tenant, l.id, err)
				s.st.Pool.Exec(scoped, `UPDATE oncall_schedules SET cal_sync_error=$3 WHERE id=$1 AND tenant_id=$2`, l.id, tenant, "sync failed: "+err.Error())
			}
		}
		cancel()
	}
}

// syncOneCalendarSchedule reads the linked calendar and writes the derived
// override (or its absence) onto the schedule row. A read/parse failure is
// returned; the schedule keeps its previous override and the static rotation
// remains the fallback.
func (s *server) syncOneCalendarSchedule(ctx context.Context, tenant, scheduleID, calendarID string) error {
	if err := googlews.RequireScope(ctx, s.secrets, tenant, googlews.ScopeCalendarOnCall); err != nil {
		// partial or legacy grant: the static rotation stays in effect and the
		// row says why, like every other fail-closed calendar state
		s.st.Pool.Exec(ctx, `UPDATE oncall_schedules SET cal_channel_id=NULL, cal_until=NULL, cal_summary='', cal_synced_at=now(), cal_sync_error=$3 WHERE id=$1 AND tenant_id=$2`,
			scheduleID, tenant, "the connected Google grant does not cover calendar read - "+err.Error())
		return nil // handled on the row; not a sweep failure
	}
	tok, err := googlews.AccessToken(ctx, s.google, s.secrets, tenant)
	if err != nil {
		return err
	}
	now := time.Now()
	events, err := s.google.ListEvents(ctx, tok, calendarID, now.Add(-time.Hour), now.Add(24*time.Hour))
	if err != nil {
		return err
	}
	var cur *googlews.CalendarEvent
	for i := range events {
		if !events[i].Start.After(now) && now.Before(events[i].End) {
			cur = &events[i] // events arrive in start order: earliest current wins
			break
		}
	}
	if cur == nil {
		_, err = s.st.Pool.Exec(ctx, `UPDATE oncall_schedules SET cal_channel_id=NULL, cal_until=NULL, cal_summary='', cal_synced_at=now(), cal_sync_error='' WHERE id=$1 AND tenant_id=$2`, scheduleID, tenant)
		return err
	}
	chRows, err := s.st.Pool.Query(ctx, `SELECT id, target FROM notification_channels WHERE tenant_id=$1`, tenant)
	if err != nil {
		return err
	}
	matched := ""
	sum := strings.ToLower(cur.Summary)
	for chRows.Next() {
		var cid, target string
		if chRows.Scan(&cid, &target) == nil && target != "" && strings.Contains(sum, strings.ToLower(target)) {
			matched = cid
			break
		}
	}
	chRows.Close()
	if matched == "" {
		_, err = s.st.Pool.Exec(ctx, `UPDATE oncall_schedules SET cal_channel_id=NULL, cal_until=NULL, cal_summary=$3, cal_synced_at=now(), cal_sync_error=$4 WHERE id=$1 AND tenant_id=$2`,
			scheduleID, tenant, cur.Summary, "current event \""+cur.Summary+"\" names no channel target in this workspace - the static rotation is in effect")
		return err
	}
	_, err = s.st.Pool.Exec(ctx, `UPDATE oncall_schedules SET cal_channel_id=$3, cal_until=$4, cal_summary=$5, cal_synced_at=now(), cal_sync_error='' WHERE id=$1 AND tenant_id=$2`,
		scheduleID, tenant, matched, cur.End, cur.Summary)
	return err
}
