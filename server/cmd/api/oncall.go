package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/rules"
)

// GET /v1/oncall: schedules with who is on duty now and until when.
func (s *server) listOnCall(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.Pool.Query(r.Context(), `SELECT id,name,anchor,shift_hours,channel_ids FROM oncall_schedules WHERE tenant_id=$1 ORDER BY name`, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	now := time.Now()
	for rows.Next() {
		var sc rules.Schedule
		if rows.Scan(&sc.ID, &sc.Name, &sc.Anchor, &sc.ShiftHours, &sc.ChannelIDs) != nil {
			continue
		}
		cur, ends, _ := rules.OnCall(sc, now)
		out = append(out, map[string]any{"id": sc.ID, "name": sc.Name, "anchor": sc.Anchor, "shift_hours": sc.ShiftHours,
			"channel_ids": sc.ChannelIDs, "on_call_channel": cur, "shift_ends": ends})
	}
	writeJSON(w, 200, out)
}

// POST /v1/oncall (admin)
func (s *server) createOnCall(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	var sc rules.Schedule
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&sc); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if err := rules.ValidateSchedule(sc); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	tenant := auth.Tenant(r)
	var n int
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM notification_channels WHERE tenant_id=$1 AND id=ANY($2)`, tenant, sc.ChannelIDs).Scan(&n)
	if n != len(sc.ChannelIDs) {
		http.Error(w, "every channel must exist in this tenant", 400)
		return
	}
	sc.ID = uuid.NewString()
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO oncall_schedules(id,tenant_id,name,anchor,shift_hours,channel_ids,created_by) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		sc.ID, tenant, sc.Name, sc.Anchor, sc.ShiftHours, sc.ChannelIDs, auth.User(r)); err != nil {
		http.Error(w, "could not save (is the name already used?)", 409)
		return
	}
	s.audit(r, "oncall.create", sc.ID, map[string]any{"name": sc.Name, "channels": len(sc.ChannelIDs)})
	writeJSON(w, 201, map[string]any{"id": sc.ID})
}

// DELETE /v1/oncall/{id} (admin): refused while an escalation step uses it.
func (s *server) deleteOnCall(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	id, tenant := r.PathValue("id"), auth.Tenant(r)
	var used int
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM escalation_steps WHERE schedule_id=$1 AND tenant_id=$2`, id, tenant).Scan(&used)
	if used > 0 {
		http.Error(w, "an escalation step uses this schedule", 409)
		return
	}
	tag, err := s.st.Pool.Exec(r.Context(), `DELETE FROM oncall_schedules WHERE id=$1 AND tenant_id=$2`, id, tenant)
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "oncall.delete", id, nil)
	w.WriteHeader(204)
}
