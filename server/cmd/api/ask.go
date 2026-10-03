package main

import (
	"encoding/json"
	"net/http"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/ask"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// POST /v1/ask {"question": "..."}: a plain-English question read as one of a few fixed read-only
// queries. Phrase matching, not an LLM (see internal/ask). The answer says how it read the question.
func (s *server) askQuestion(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Question string `json:"question"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	q, ok := ask.Parse(in.Question)
	if !ok {
		writeJSON(w, 422, map[string]any{"error": "I did not understand that. I match a small set of question shapes; I am not a language model.", "examples": ask.Examples})
		return
	}
	tenant, ctx := auth.Tenant(r), r.Context()
	out := map[string]any{"interpreted_as": ask.Describe(q), "method": "rule-based phrase matching, read-only"}
	device := func() (id, name string, ok bool) {
		rows, err := s.st.Pool.Query(ctx, `SELECT id, name FROM devices WHERE tenant_id=$1 AND (lower(name)=$2 OR lower(id)=$2) LIMIT 2`, tenant, q.Device)
		if err != nil {
			return "", "", false
		}
		defer rows.Close()
		n := 0
		for rows.Next() {
			rows.Scan(&id, &name)
			n++
		}
		return id, name, n == 1
	}
	switch q.Kind {
	case ask.OpenAlerts, ask.DeviceAlerts:
		devID := ""
		if q.Kind == ask.DeviceAlerts {
			id, name, ok := device()
			if !ok {
				writeJSON(w, 404, map[string]any{"error": "no single device is named " + q.Device + " (use its exact name or id)", "interpreted_as": out["interpreted_as"]})
				return
			}
			devID = id
			out["device"] = map[string]string{"id": id, "name": name}
		}
		rows, err := s.st.Pool.Query(ctx,
			`SELECT id, severity, message, status, created_at, COALESCE(device_id,'') FROM alerts
			 WHERE tenant_id=$1 AND status IN ('open','acknowledged') AND NOT shelved
			   AND ($2='' OR severity=$2) AND ($3='' OR device_id=$3) ORDER BY created_at DESC LIMIT 50`, tenant, q.Severity, devID)
		if err != nil {
			http.Error(w, "db", 500)
			return
		}
		defer rows.Close()
		list := []map[string]any{}
		for rows.Next() {
			var id, sev, msg, st, dev string
			var at any
			if rows.Scan(&id, &sev, &msg, &st, &at, &dev) == nil {
				list = append(list, map[string]any{"id": id, "severity": sev, "message": msg, "status": st, "created_at": at, "device_id": dev})
			}
		}
		out["kind"], out["rows"] = "alerts", list
	case ask.LatestValue:
		id, name, ok := device()
		if !ok {
			writeJSON(w, 404, map[string]any{"error": "no single device is named " + q.Device + " (use its exact name or id)", "interpreted_as": out["interpreted_as"]})
			return
		}
		var v float64
		var unit, quality string
		var at any
		err := s.st.Pool.QueryRow(ctx, `SELECT value, COALESCE(unit,''), COALESCE(quality,''), observed_at FROM telemetry
			WHERE tenant_id=$1 AND device_id=$2 AND lower(point_id)=$3 ORDER BY observed_at DESC LIMIT 1`, tenant, id, q.Point).Scan(&v, &unit, &quality, &at)
		if err != nil {
			writeJSON(w, 404, map[string]any{"error": "no readings for point " + q.Point + " on " + name, "interpreted_as": out["interpreted_as"]})
			return
		}
		out["kind"], out["device"] = "value", map[string]string{"id": id, "name": name}
		out["value"], out["unit"], out["quality"], out["observed_at"] = v, unit, quality, at
	case ask.OfflineDevice, ask.TaggedDevices:
		query := `SELECT d.id, d.name FROM devices d WHERE d.tenant_id=$1 AND NOT EXISTS (
			SELECT 1 FROM telemetry te WHERE te.device_id=d.id AND te.observed_at > now() - interval '15 minutes') ORDER BY d.name LIMIT 100`
		args := []any{tenant}
		if q.Kind == ask.TaggedDevices {
			query = `SELECT d.id, d.name FROM devices d WHERE d.tenant_id=$1 AND $2 = ANY(d.tags) ORDER BY d.name LIMIT 100`
			args = append(args, q.Tag)
		}
		rows, err := s.st.Pool.Query(ctx, query, args...)
		if err != nil {
			http.Error(w, "db", 500)
			return
		}
		defer rows.Close()
		list := []map[string]string{}
		for rows.Next() {
			var id, name string
			if rows.Scan(&id, &name) == nil {
				list = append(list, map[string]string{"id": id, "name": name})
			}
		}
		out["kind"], out["rows"] = "devices", list
	}
	writeJSON(w, 200, out)
}
