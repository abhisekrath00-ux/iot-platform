package main

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/control"
)

// A control target is one allowlisted thing a flow may ask to change: a fixed
// device, point and kind, with a value range or an explicit set of allowed values.
// Targets are created disabled. Nothing here actuates: a target only bounds what
// can be REQUESTED, and every request still needs a second human to approve it.

type controlTarget = control.Target

func validateTarget(t *controlTarget) error {
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" || len(t.Name) > 80 {
		return errors.New("name is required, 80 characters at most")
	}
	if t.Kind != "modbus_write" && t.Kind != "alarm_output" {
		return errors.New(`kind must be "modbus_write" or "alarm_output"`)
	}
	if t.ApprovalMode == "" {
		t.ApprovalMode = "approval"
	}
	if t.ApprovalMode != "approval" && t.ApprovalMode != "automatic" {
		return errors.New(`approval_mode must be "approval" or "automatic"`)
	}
	if t.ApprovalMode == "automatic" && t.Kind != "alarm_output" {
		return errors.New(`automatic mode is only for low-risk alarm outputs (kind "alarm_output"); Modbus writes always need a second person's approval`)
	}
	if t.Kind == "alarm_output" {
		// An annunciator is on or off: nothing else is ever requested.
		t.AllowedValues, t.Min, t.Max = []float64{0, 1}, nil, nil
	}
	if t.GatewayID == "" || t.DeviceID == "" || t.PointID == "" {
		return errors.New("gateway_id, device_id and point_id are required")
	}
	if t.MaxPerHour == 0 {
		t.MaxPerHour = 6
	}
	if t.MaxPerHour < 1 || t.MaxPerHour > 60 {
		return errors.New("max_per_hour must be 1-60")
	}
	finite := func(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }
	if len(t.AllowedValues) > 0 {
		if len(t.AllowedValues) > 20 {
			return errors.New("at most 20 allowed values")
		}
		for _, v := range t.AllowedValues {
			if !finite(v) {
				return errors.New("allowed values must be finite numbers")
			}
		}
		t.Min, t.Max = nil, nil
		return nil
	}
	if t.Min == nil || t.Max == nil || !finite(*t.Min) || !finite(*t.Max) || *t.Min > *t.Max {
		return errors.New("give allowed_values, or min and max with min <= max: a target is never open-ended")
	}
	return nil
}

const targetCols = `id, name, kind, gateway_id, device_id, point_id, min_value, max_value, allowed_values, max_per_hour, approval_mode, enabled`

type rowScanner interface{ Scan(...any) error }

func scanTarget(r rowScanner, t *controlTarget) error {
	return r.Scan(&t.ID, &t.Name, &t.Kind, &t.GatewayID, &t.DeviceID, &t.PointID, &t.Min, &t.Max, &t.AllowedValues, &t.MaxPerHour, &t.ApprovalMode, &t.Enabled)
}

func (s *server) listControlTargets(w http.ResponseWriter, r *http.Request) {
	if auth.Role(r) != "admin" && auth.Role(r) != "operator" {
		http.Error(w, "admin or operator required", 403)
		return
	}
	rows, err := s.st.Pool.Query(r.Context(), `SELECT `+targetCols+` FROM control_targets WHERE tenant_id=$1 ORDER BY name`, auth.Tenant(r))
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer rows.Close()
	out := []controlTarget{}
	for rows.Next() {
		var t controlTarget
		if scanTarget(rows, &t) == nil {
			out = append(out, t)
		}
	}
	writeJSON(w, 200, out)
}

func (s *server) createControlTarget(w http.ResponseWriter, r *http.Request) {
	if auth.Role(r) != "admin" || auth.ViaKey(r) {
		http.Error(w, "an admin session is required to define control targets", 403)
		return
	}
	var t controlTarget
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if err := validateTarget(&t); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var ok bool
	if err := s.st.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM devices WHERE id=$1 AND tenant_id=$2 AND gateway_id=$3)
	    AND EXISTS(SELECT 1 FROM points WHERE device_id=$1 AND id=$4)`, t.DeviceID, auth.Tenant(r), t.GatewayID, t.PointID).Scan(&ok); err != nil || !ok {
		http.Error(w, "no such device, gateway and point in this tenant", 400)
		return
	}
	t.ID, t.Enabled = uuid.NewString(), false
	_, err := s.st.Pool.Exec(r.Context(), `INSERT INTO control_targets(id,tenant_id,name,kind,gateway_id,device_id,point_id,min_value,max_value,allowed_values,max_per_hour,approval_mode,created_by)
	    VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, t.ID, auth.Tenant(r), t.Name, t.Kind, t.GatewayID, t.DeviceID, t.PointID, t.Min, t.Max, nilIfEmpty(t.AllowedValues), t.MaxPerHour, t.ApprovalMode, auth.User(r))
	if err != nil {
		if strings.Contains(err.Error(), "control_targets_tenant_id_name_key") {
			http.Error(w, "a target with this name exists", 409)
			return
		}
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "control_target.create", t.ID, map[string]any{"name": t.Name, "device": t.DeviceID, "point": t.PointID, "kind": t.Kind, "approval_mode": t.ApprovalMode})
	writeJSON(w, 201, t)
}

func nilIfEmpty(v []float64) any {
	if len(v) == 0 {
		return nil
	}
	return v
}

// setControlTargetEnabled is the per-target kill switch. Disabling is immediate:
// requests for a disabled target are refused.
func (s *server) setControlTargetEnabled(w http.ResponseWriter, r *http.Request) {
	if auth.Role(r) != "admin" || auth.ViaKey(r) {
		http.Error(w, "an admin session is required", 403)
		return
	}
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&in) != nil || in.Enabled == nil {
		http.Error(w, "enabled (true/false) required", 400)
		return
	}
	tag, err := s.st.Pool.Exec(r.Context(), `UPDATE control_targets SET enabled=$1 WHERE id=$2 AND tenant_id=$3`, *in.Enabled, r.PathValue("id"), auth.Tenant(r))
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "control_target.enabled", r.PathValue("id"), map[string]any{"enabled": *in.Enabled})
	writeJSON(w, 200, map[string]any{"id": r.PathValue("id"), "enabled": *in.Enabled})
}

func (s *server) deleteControlTarget(w http.ResponseWriter, r *http.Request) {
	if auth.Role(r) != "admin" || auth.ViaKey(r) {
		http.Error(w, "an admin session is required", 403)
		return
	}
	tag, err := s.st.Pool.Exec(r.Context(), `DELETE FROM control_targets WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), auth.Tenant(r))
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "control_target.delete", r.PathValue("id"), nil)
	w.WriteHeader(204)
}

// setControlTargetMode is the admin's choice between "approval" (default) and "automatic".
// Automatic is only ever valid for alarm outputs, and every change is audited with who and when.
func (s *server) setControlTargetMode(w http.ResponseWriter, r *http.Request) {
	if auth.Role(r) != "admin" || auth.ViaKey(r) {
		http.Error(w, "an admin session is required", 403)
		return
	}
	var in struct {
		Mode string `json:"approval_mode"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&in) != nil || (in.Mode != "approval" && in.Mode != "automatic") {
		http.Error(w, `approval_mode must be "approval" or "automatic"`, 400)
		return
	}
	var kind string
	if s.st.Pool.QueryRow(r.Context(), `SELECT kind FROM control_targets WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), auth.Tenant(r)).Scan(&kind) != nil {
		http.Error(w, "not found", 404)
		return
	}
	if in.Mode == "automatic" && kind != "alarm_output" {
		http.Error(w, "automatic mode is only for low-risk alarm outputs; this target always needs approval", 422)
		return
	}
	if _, err := s.st.Pool.Exec(r.Context(), `UPDATE control_targets SET approval_mode=$1 WHERE id=$2 AND tenant_id=$3`, in.Mode, r.PathValue("id"), auth.Tenant(r)); err != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "control_target.approval_mode", r.PathValue("id"), map[string]any{"approval_mode": in.Mode})
	writeJSON(w, 200, map[string]any{"id": r.PathValue("id"), "approval_mode": in.Mode})
}
