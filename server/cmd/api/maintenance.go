package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// Maintenance windows quiet warning and info alerts for a device or an asset (and the devices under
// it) for a limited time, so planned work does not page people. Alerts are still recorded and shown
// with a "shelved" mark. Critical alerts are never quieted. If a shelved alert is still open when
// the window ends, one notification goes out and escalation starts to apply. A window lasts 7 days
// at most and can be ended early.

type maintenanceWindow struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	DeviceID *string    `json:"device_id"`
	AssetID  *string    `json:"asset_id"`
	StartsAt time.Time  `json:"starts_at"`
	EndsAt   time.Time  `json:"ends_at"`
	EndedAt  *time.Time `json:"ended_at"`
	Active   bool       `json:"active"`
	By       string     `json:"created_by"`
}

func (s *server) listMaintenance(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.Pool.Query(r.Context(), `SELECT id,name,device_id,asset_id,starts_at,ends_at,ended_at,created_by,
	    (ended_at IS NULL AND now()>=starts_at AND now()<ends_at)
	    FROM maintenance_windows WHERE tenant_id=$1 AND (ended_at IS NULL AND ends_at > now() OR ends_at > now() - interval '7 days')
	    ORDER BY starts_at DESC LIMIT 100`, auth.Tenant(r))
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer rows.Close()
	out := []maintenanceWindow{}
	for rows.Next() {
		var m maintenanceWindow
		if rows.Scan(&m.ID, &m.Name, &m.DeviceID, &m.AssetID, &m.StartsAt, &m.EndsAt, &m.EndedAt, &m.By, &m.Active) == nil {
			out = append(out, m)
		}
	}
	writeJSON(w, 200, out)
}

func (s *server) createMaintenance(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		Name     string    `json:"name"`
		DeviceID string    `json:"device_id"`
		AssetID  string    `json:"asset_id"`
		StartsAt time.Time `json:"starts_at"`
		EndsAt   time.Time `json:"ends_at"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil {
		http.Error(w, "bad json", 400)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 80 {
		http.Error(w, "name is required, 80 characters at most", 400)
		return
	}
	if (in.DeviceID == "") == (in.AssetID == "") {
		http.Error(w, "give exactly one of device_id or asset_id", 400)
		return
	}
	if in.StartsAt.IsZero() {
		in.StartsAt = time.Now()
	}
	if !in.EndsAt.After(in.StartsAt) || in.EndsAt.After(in.StartsAt.Add(7*24*time.Hour)) {
		http.Error(w, "ends_at must be after starts_at and within 7 days of it", 400)
		return
	}
	if in.EndsAt.Before(time.Now()) {
		http.Error(w, "ends_at is in the past", 400)
		return
	}
	tenant := auth.Tenant(r)
	var dev, asset any
	var ok bool
	if in.DeviceID != "" {
		dev = in.DeviceID
		s.st.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM devices WHERE id=$1 AND tenant_id=$2)`, in.DeviceID, tenant).Scan(&ok)
	} else {
		asset = in.AssetID
		s.st.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM assets WHERE id=$1 AND tenant_id=$2)`, in.AssetID, tenant).Scan(&ok)
	}
	if !ok {
		http.Error(w, "unknown device or asset in this tenant", 400)
		return
	}
	id := uuid.NewString()
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO maintenance_windows(id,tenant_id,name,device_id,asset_id,starts_at,ends_at,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		id, tenant, in.Name, dev, asset, in.StartsAt, in.EndsAt, auth.User(r)); err != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "maintenance.create", id, map[string]any{"name": in.Name, "device": in.DeviceID, "asset": in.AssetID, "ends_at": in.EndsAt})
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *server) endMaintenance(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	tag, err := s.st.Pool.Exec(r.Context(), `UPDATE maintenance_windows SET ended_at=now() WHERE id=$1 AND tenant_id=$2 AND ended_at IS NULL`, r.PathValue("id"), auth.Tenant(r))
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "not found or already ended", 404)
		return
	}
	s.audit(r, "maintenance.end", r.PathValue("id"), nil)
	writeJSON(w, 200, map[string]any{"id": r.PathValue("id"), "ended": true})
}
