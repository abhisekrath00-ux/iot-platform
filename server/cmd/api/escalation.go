package main

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/rules"
)

// GET /v1/escalation: the tenant's escalation policy.
func (s *server) getEscalation(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT severity, step, after_minutes, channel_id FROM escalation_steps WHERE tenant_id=$1 ORDER BY severity, step`, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []rules.Step{}
	for rows.Next() {
		var st rules.Step
		if rows.Scan(&st.Severity, &st.Step, &st.AfterMinutes, &st.ChannelID) == nil {
			out = append(out, st)
		}
	}
	var rep rules.Repeat
	rows.Close()
	_ = s.st.Pool.QueryRow(r.Context(), `SELECT repeat_every_minutes, repeat_max FROM escalation_settings WHERE tenant_id=$1`, auth.Tenant(r)).Scan(&rep.EveryMinutes, &rep.Max)
	writeJSON(w, 200, map[string]any{"steps": out, "repeat": rep})
}

// PUT /v1/escalation (admin): replace the policy. An empty list switches escalation off.
func (s *server) putEscalation(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	var in struct {
		Steps  []rules.Step  `json:"steps"`
		Repeat *rules.Repeat `json:"repeat"` // omitted: keep the current setting
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if err := rules.ValidateSteps(in.Steps); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if in.Repeat != nil {
		if err := rules.ValidateRepeat(*in.Repeat); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	}
	tenant := auth.Tenant(r)
	tx, err := s.st.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(), `DELETE FROM escalation_steps WHERE tenant_id=$1`, tenant); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	for _, st := range in.Steps {
		var n int
		if tx.QueryRow(r.Context(), `SELECT count(*) FROM notification_channels WHERE id=$1 AND tenant_id=$2 AND enabled`, st.ChannelID, tenant).Scan(&n); n == 0 {
			http.Error(w, "channel "+st.ChannelID+" not found or disabled", 400)
			return
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO escalation_steps(id,tenant_id,severity,step,after_minutes,channel_id) VALUES($1,$2,$3,$4,$5,$6)`,
			uuid.NewString(), tenant, st.Severity, st.Step, st.AfterMinutes, st.ChannelID); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	if in.Repeat != nil {
		if _, err := tx.Exec(r.Context(), `INSERT INTO escalation_settings(tenant_id,repeat_every_minutes,repeat_max) VALUES($1,$2,$3)
			ON CONFLICT (tenant_id) DO UPDATE SET repeat_every_minutes=EXCLUDED.repeat_every_minutes, repeat_max=EXCLUDED.repeat_max`, tenant, in.Repeat.EveryMinutes, in.Repeat.Max); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit(r, "escalation.update", tenant, map[string]any{"steps": len(in.Steps)})
	s.getEscalation(w, r)
}
