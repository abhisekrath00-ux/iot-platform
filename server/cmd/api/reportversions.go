package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/report"
)

// Report versions. Editing a report (or restoring an older version) first saves the
// current state as a numbered version, then writes the new state with version+1. Nothing
// is ever overwritten, so any earlier state can be restored; restoring creates a new
// version rather than rewriting history.

const maxReportVersions = 100

var errReportOutsideCustomer = errors.New("this report is shared with a customer and uses a device outside it")

type reportState struct {
	name       string
	definition []byte
	cron       *string
	channelID  *string
}

// writeReportVersion saves the report's current state as a version and replaces it with next.
func (s *server) writeReportVersion(ctx context.Context, tenant, id, user string, next reportState) (newVersion int, found bool, err error) {
	tx, err := s.st.Pool.Begin(ctx)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback(ctx)
	var cur reportState
	var ver int
	var shared *string
	err = tx.QueryRow(ctx, `SELECT name, definition, schedule_cron, channel_id, version, customer_id FROM reports WHERE id=$1 AND tenant_id=$2 FOR UPDATE`, id, tenant).
		Scan(&cur.name, &cur.definition, &cur.cron, &cur.channelID, &ver, &shared)
	if err == pgx.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if shared != nil { // a report shared with a customer may only use that customer's devices
		var nd struct {
			Metrics []struct {
				DeviceID string `json:"device_id"`
			} `json:"metrics"`
		}
		json.Unmarshal(next.definition, &nd)
		for _, m := range nd.Metrics {
			if !s.deviceInScope(ctx, tenant, *shared, m.DeviceID) {
				return 0, false, errReportOutsideCustomer
			}
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO report_versions(report_id,version,name,definition,schedule_cron,channel_id,superseded_by) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		id, ver, cur.name, cur.definition, cur.cron, cur.channelID, user); err != nil {
		return 0, false, err
	}
	// keep the newest maxReportVersions
	if _, err = tx.Exec(ctx, `DELETE FROM report_versions WHERE report_id=$1 AND version <= $2`, id, ver-maxReportVersions); err != nil {
		return 0, false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE reports SET name=$1, definition=$2, schedule_cron=$3, channel_id=$4, version=$5 WHERE id=$6 AND tenant_id=$7`,
		next.name, next.definition, next.cron, next.channelID, ver+1, id, tenant); err != nil {
		return 0, false, err
	}
	return ver + 1, true, tx.Commit(ctx)
}

// PUT /v1/reports/{id} (admin, operator): replace name, definition, schedule and channel.
func (s *server) updateReport(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		Name         string            `json:"name"`
		Definition   report.Definition `json:"definition"`
		ScheduleCron string            `json:"schedule_cron"`
		ChannelID    string            `json:"channel_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 128 || strings.ContainsAny(name, "<>\x00") {
		http.Error(w, "name invalid", 400)
		return
	}
	if err := report.Validate(in.Definition); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var cron, channelID *string
	if in.ScheduleCron != "" {
		if _, err := report.NextRun(in.ScheduleCron, time.Now()); err != nil {
			http.Error(w, "schedule_cron: "+err.Error(), 400)
			return
		}
		cron = &in.ScheduleCron
	}
	tenant := auth.Tenant(r)
	if in.ChannelID != "" {
		var n int
		if s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM notification_channels WHERE id=$1 AND tenant_id=$2`, in.ChannelID, tenant).Scan(&n); n == 0 {
			http.Error(w, "channel not found", 400)
			return
		}
		channelID = &in.ChannelID
	}
	def, _ := json.Marshal(in.Definition)
	id := r.PathValue("id")
	v, found, err := s.writeReportVersion(r.Context(), tenant, id, auth.User(r), reportState{name, def, cron, channelID})
	if errors.Is(err, errReportOutsideCustomer) {
		http.Error(w, err.Error(), 409)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if !found {
		http.Error(w, "report not found", 404)
		return
	}
	s.audit(r, "report.update", id, map[string]any{"name": name, "version": v})
	writeJSON(w, 200, map[string]any{"id": id, "version": v})
}

// GET /v1/reports/{id}/versions: the current version number and the saved earlier versions.
func (s *server) listReportVersions(w http.ResponseWriter, r *http.Request) {
	id, tenant := r.PathValue("id"), auth.Tenant(r)
	var cur int
	if s.st.Pool.QueryRow(r.Context(), `SELECT version FROM reports WHERE id=$1 AND tenant_id=$2`, id, tenant).Scan(&cur) != nil {
		http.Error(w, "report not found", 404)
		return
	}
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT version, name, definition, schedule_cron, superseded_by, superseded_at FROM report_versions WHERE report_id=$1 ORDER BY version DESC`, id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var ver int
		var name, by string
		var def []byte
		var cron *string
		var at time.Time
		if rows.Scan(&ver, &name, &def, &cron, &by, &at) == nil {
			out = append(out, map[string]any{"version": ver, "name": name, "definition": json.RawMessage(def), "schedule_cron": cron, "replaced_by": by, "replaced_at": at})
		}
	}
	writeJSON(w, 200, map[string]any{"current": cur, "versions": out})
}

// POST /v1/reports/{id}/versions/{v}/restore (admin, operator): make an earlier version current
// again, as a new version.
func (s *server) restoreReportVersion(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	id, tenant := r.PathValue("id"), auth.Tenant(r)
	ver, err := strconv.Atoi(r.PathValue("v"))
	if err != nil || ver < 1 {
		http.Error(w, "bad version", 400)
		return
	}
	var old reportState
	// only a version of a report this tenant owns
	err = s.st.Pool.QueryRow(r.Context(),
		`SELECT v.name, v.definition, v.schedule_cron, v.channel_id FROM report_versions v JOIN reports p ON p.id=v.report_id
		 WHERE v.report_id=$1 AND v.version=$2 AND p.tenant_id=$3`, id, ver, tenant).Scan(&old.name, &old.definition, &old.cron, &old.channelID)
	if err != nil {
		http.Error(w, "version not found", 404)
		return
	}
	// the channel may have been deleted since; never restore a dangling or foreign channel
	if old.channelID != nil {
		var n int
		if s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM notification_channels WHERE id=$1 AND tenant_id=$2`, *old.channelID, tenant).Scan(&n); n == 0 {
			old.channelID = nil
		}
	}
	v, found, err := s.writeReportVersion(r.Context(), tenant, id, auth.User(r), old)
	if errors.Is(err, errReportOutsideCustomer) {
		http.Error(w, err.Error(), 409)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if !found {
		http.Error(w, "report not found", 404)
		return
	}
	s.audit(r, "report.restore", id, map[string]any{"restored": ver, "version": v})
	writeJSON(w, 200, map[string]any{"id": id, "version": v, "restored_from": ver})
}
