package main

// Flow versioning: drafts, publish, rollback, and simulation over recorded
// telemetry. The runtime engine executes only the published version, so a
// bad edit can never fire until explicitly published, and rollback is a
// pointer flip back to any earlier version - both are audited.

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
	"github.com/google/uuid"
)

// flowOwned verifies the flow belongs to the caller's tenant.
func (s *server) flowOwned(r *http.Request, id string) bool {
	var ok bool
	if err := s.st.Pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM flows WHERE id=$1 AND tenant_id=$2)`,
		id, auth.Tenant(r)).Scan(&ok); err != nil || !ok {
		return false
	}
	return true
}

// checkFlowChannels verifies every notify step references a channel the
// tenant owns (same rule as flow creation).
func (s *server) checkFlowChannels(r *http.Request, d flow.Definition) bool {
	for _, slot := range d.ChannelSlots() {
		var ok bool
		if err := s.st.Pool.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM notification_channels WHERE id=$1 AND tenant_id=$2)`,
			*slot, auth.Tenant(r)).Scan(&ok); err != nil || !ok {
			return false
		}
	}
	return true
}

func (s *server) listFlowVersions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.flowOwned(r, id) {
		http.Error(w, "flow not found", 404)
		return
	}
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT id, version, status, created_by, created_at, published_at
		 FROM flow_versions WHERE flow_id=$1 ORDER BY version DESC`, id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var vid, status, by string
		var ver int
		var created time.Time
		var published *time.Time
		rows.Scan(&vid, &ver, &status, &by, &created, &published)
		out = append(out, map[string]any{"id": vid, "version": ver, "status": status,
			"created_by": by, "created_at": created, "published_at": published})
	}
	writeJSON(w, 200, out)
}

func (s *server) createFlowDraft(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	id := r.PathValue("id")
	if !s.flowOwned(r, id) {
		http.Error(w, "flow not found", 404)
		return
	}
	var in struct {
		Definition flow.Definition `json:"definition"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if err := flow.Validate(in.Definition); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if !s.gateFunctionNodes(w, r, in.Definition) {
		return
	}
	if !s.checkFlowChannels(r, in.Definition) {
		http.Error(w, "notify channel not found for this tenant", 400)
		return
	}
	def, _ := json.Marshal(in.Definition)
	verID := uuid.NewString()
	var ver int
	if err := s.st.Pool.QueryRow(r.Context(),
		`INSERT INTO flow_versions(id,flow_id,tenant_id,version,definition,status,created_by)
		 SELECT $1, $2, $3, COALESCE(MAX(version),0)+1, $4, 'draft', $5
		 FROM flow_versions WHERE flow_id=$2 RETURNING version`,
		verID, id, auth.Tenant(r), def, auth.User(r)).Scan(&ver); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit(r, "flow.draft", id, map[string]any{"version": ver})
	writeJSON(w, 201, map[string]any{"id": verID, "version": ver, "status": "draft"})
}

// setPublished flips the published pointer to the given version (shared by
// publish and rollback). The previous published version is archived.
func (s *server) setPublished(w http.ResponseWriter, r *http.Request, flowID string, version int) bool {
	var verID string
	if err := s.st.Pool.QueryRow(r.Context(),
		`SELECT id FROM flow_versions WHERE flow_id=$1 AND tenant_id=$2 AND version=$3`,
		flowID, auth.Tenant(r), version).Scan(&verID); err != nil {
		http.Error(w, "version not found", 404)
		return false
	}
	tx, err := s.st.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return false
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(),
		`UPDATE flow_versions SET status='archived' WHERE flow_id=$1 AND status='published'`, flowID); err != nil {
		http.Error(w, err.Error(), 500)
		return false
	}
	if _, err := tx.Exec(r.Context(),
		`UPDATE flow_versions SET status='published', published_at=now() WHERE id=$1`, verID); err != nil {
		http.Error(w, err.Error(), 500)
		return false
	}
	if _, err := tx.Exec(r.Context(),
		`UPDATE flows SET published_version_id=$1 WHERE id=$2`, verID, flowID); err != nil {
		http.Error(w, err.Error(), 500)
		return false
	}
	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, err.Error(), 500)
		return false
	}
	return true
}

func (s *server) publishFlow(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	id := r.PathValue("id")
	if !s.flowOwned(r, id) {
		http.Error(w, "flow not found", 404)
		return
	}
	var in struct {
		Version int `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Version < 1 {
		http.Error(w, "version required", 400)
		return
	}
	if !s.setPublished(w, r, id, in.Version) {
		return
	}
	s.audit(r, "flow.publish", id, map[string]any{"version": in.Version})
	writeJSON(w, 200, map[string]any{"published": in.Version})
}

func (s *server) rollbackFlow(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	id := r.PathValue("id")
	if !s.flowOwned(r, id) {
		http.Error(w, "flow not found", 404)
		return
	}
	var in struct {
		Version int `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Version < 1 {
		http.Error(w, "version required", 400)
		return
	}
	if !s.setPublished(w, r, id, in.Version) {
		return
	}
	s.audit(r, "flow.rollback", id, map[string]any{"version": in.Version})
	writeJSON(w, 200, map[string]any{"rolled_back_to": in.Version})
}

// simulateFlow replays recorded telemetry through a candidate definition.
// Read-only: nothing is dispatched and nothing is written to flow_runs.
func (s *server) simulateFlow(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Definition flow.Definition `json:"definition"`
		Hours      int             `json:"hours"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if err := flow.Validate(in.Definition); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if in.Hours < 1 {
		in.Hours = 24
	}
	if in.Hours > 720 {
		in.Hours = 720
	}
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT observed_at, value FROM telemetry
		 WHERE tenant_id=$1 AND device_id=$2 AND point_id=$3
		   AND observed_at > now() - make_interval(hours => $4)
		 ORDER BY observed_at LIMIT 10000`,
		auth.Tenant(r), in.Definition.Trig().DeviceID, in.Definition.Trig().PointID, in.Hours)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	var readings []flow.Reading
	for rows.Next() {
		var rd flow.Reading
		if rows.Scan(&rd.ObservedAt, &rd.Value) == nil {
			readings = append(readings, rd)
		}
	}
	rows.Close()

	events := flow.Simulate(in.Definition, readings)
	truncated := false
	if len(events) > 100 {
		events = events[:100]
		truncated = true
	}
	writeJSON(w, 200, map[string]any{
		"window_hours": in.Hours,
		"readings":     len(readings),
		"triggered":    len(events),
		"truncated":    truncated,
		"events":       events,
	})
}
