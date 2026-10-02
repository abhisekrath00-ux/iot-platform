package main

import (
	"encoding/json"
	"net/http"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// getRetention returns this tenant's retention policy. Null fields mean
// "inherit the deployment default" (RAW_RETENTION_DAYS, hourly kept forever).
func (s *server) getRetention(w http.ResponseWriter, r *http.Request) {
	var raw, hourly *int
	err := s.st.Pool.QueryRow(r.Context(),
		`SELECT raw_days, hourly_days FROM tenant_retention WHERE tenant_id=$1`, auth.Tenant(r)).Scan(&raw, &hourly)
	if err != nil { // no row yet
		raw, hourly = nil, nil
	}
	writeJSON(w, 200, map[string]any{"raw_days": raw, "hourly_days": hourly})
}

// putRetention sets this tenant's policy (admin). Shortening retention is
// destructive on the next job run, so it is audited with the old and new values.
func (s *server) putRetention(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	var in struct {
		RawDays    *int `json:"raw_days"`
		HourlyDays *int `json:"hourly_days"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if in.RawDays != nil && (*in.RawDays < 1 || *in.RawDays > 3650) {
		http.Error(w, "raw_days must be 1-3650 or null", 400)
		return
	}
	if in.HourlyDays != nil && (*in.HourlyDays < 30 || *in.HourlyDays > 7300) {
		http.Error(w, "hourly_days must be 30-7300 or null", 400)
		return
	}
	if in.RawDays != nil && in.HourlyDays != nil && *in.HourlyDays < *in.RawDays {
		http.Error(w, "hourly_days must not be shorter than raw_days", 400)
		return
	}
	var oldRaw, oldHourly *int
	s.st.Pool.QueryRow(r.Context(), `SELECT raw_days, hourly_days FROM tenant_retention WHERE tenant_id=$1`, auth.Tenant(r)).Scan(&oldRaw, &oldHourly)
	if _, err := s.st.Pool.Exec(r.Context(),
		`INSERT INTO tenant_retention(tenant_id, raw_days, hourly_days, updated_by) VALUES($1,$2,$3,$4)
		 ON CONFLICT (tenant_id) DO UPDATE SET raw_days=$2, hourly_days=$3, updated_at=now(), updated_by=$4`,
		auth.Tenant(r), in.RawDays, in.HourlyDays, auth.User(r)); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit(r, "retention.update", auth.Tenant(r), map[string]any{
		"old_raw_days": oldRaw, "old_hourly_days": oldHourly, "raw_days": in.RawDays, "hourly_days": in.HourlyDays})
	writeJSON(w, 200, map[string]any{"raw_days": in.RawDays, "hourly_days": in.HourlyDays})
}
