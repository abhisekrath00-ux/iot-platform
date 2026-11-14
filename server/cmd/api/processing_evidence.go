package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/ingestprocessing"
	"github.com/jackc/pgx/v5"
)

// Inspect a single retained identity only. No activation, schema creation,
// execution, takeover, replay or automatic retry is exposed by this endpoint.
// The view omits definitions, channel destinations, message bodies and secrets.
func (s *server) processingEvidence(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !requireRole(w, r, "admin") {
		return
	}
	if auth.ViaKey(r) || scopeOf(r) != "" {
		http.Error(w, "workspace interactive admin required", 403)
		return
	}
	q := r.URL.Query()
	for k, v := range q {
		if (k != "event_id" && k != "observed_at") || len(v) != 1 {
			http.Error(w, "one exact event_id and observed_at are required", 400)
			return
		}
	}
	event, stamp := q.Get("event_id"), q.Get("observed_at")
	if strings.TrimSpace(event) == "" || len(event) > 256 || strings.IndexFunc(event, unicode.IsControl) >= 0 || len(stamp) > 40 {
		http.Error(w, "bounded event identity required", 400)
		return
	}
	at, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil || at.Year() < 1970 || !at.Equal(at.Truncate(time.Microsecond)) {
		http.Error(w, "exact RFC3339 observation time with at most microsecond precision required", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var present bool
	if err = s.st.Pool.QueryRow(ctx, "SELECT to_regclass('ingest_processing_v1') IS NOT NULL").Scan(&present); err != nil {
		http.Error(w, "processing evidence unavailable; no action was taken", 503)
		return
	}
	if !present {
		writeJSON(w, 200, map[string]any{"status": "staged_store_absent", "read_only": true, "execution_available": false})
		return
	}
	rec, err := (ingestprocessing.Engine{Pool: s.st.Pool}).Read(ctx, auth.Tenant(r), event, at)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "no retained evidence for this tenant and exact identity; this does not prove the input was never received or processed", 404)
		return
	}
	if errors.Is(err, ingestprocessing.ErrInput) || errors.Is(err, ingestprocessing.ErrPlan) {
		writeJSON(w, 409, map[string]any{"status": "evidence_integrity_unknown", "read_only": true, "execution_available": false, "message": "Retained evidence did not validate. Do not execute, retry or infer delivery from this record."})
		return
	}
	if err != nil {
		http.Error(w, "processing evidence unavailable; current state is unknown and no action was taken", 503)
		return
	}
	p := rec.Plan
	writeJSON(w, 200, map[string]any{"status": "retained_evidence", "read_only": true, "execution_available": false, "event_id": event, "observed_at": p.Envelope.ObservedAt, "gateway_id": p.Envelope.GatewayID, "device_id": p.Envelope.DeviceID, "point_id": p.Envelope.PointID, "policy": p.Policy, "selected_at": p.SelectedAt, "state": rec.State, "attempt_id": rec.AttemptID, "quarantine": rec.Quarantine, "blocked": p.Blocked, "intent_count": len(p.Intents), "flow_count": len(p.Flows), "rule_count": len(p.Rules), "channel_count": len(p.Channels)})
}
