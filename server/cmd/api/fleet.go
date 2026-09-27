package main

// Fleet rollout API: releases, staged campaigns with deterministic cohorts,
// failure-threshold halts, and rollback. Delivery to gateways (retained
// broker messages on t/<tenant>/g/<serial>/fleet) and edge auto-ACK are the
// edge-agent slice documented in docs/fleet.md; this file owns the
// platform-side campaign state.

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/fleet"
	"github.com/google/uuid"
)

func (s *server) createRelease(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		Version string `json:"version"`
		SHA256  string `json:"artifact_sha256"`
		Notes   string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if in.Version == "" || len(in.Version) > 64 {
		http.Error(w, "version required (<=64 chars)", 400)
		return
	}
	id := uuid.NewString()
	if _, err := s.st.Pool.Exec(r.Context(),
		`INSERT INTO fleet_releases(id,tenant_id,version,artifact_sha256,notes,created_by) VALUES($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (tenant_id,version) DO NOTHING`,
		id, auth.Tenant(r), in.Version, in.SHA256, in.Notes, auth.User(r)); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit(r, "fleet.release", id, map[string]any{"version": in.Version})
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *server) listReleases(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT id, version, COALESCE(artifact_sha256,''), COALESCE(notes,''), created_at
		 FROM fleet_releases WHERE tenant_id=$1 ORDER BY created_at DESC`, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, v, sha, notes string
		var created time.Time
		rows.Scan(&id, &v, &sha, &notes, &created)
		out = append(out, map[string]any{"id": id, "version": v, "artifact_sha256": sha, "notes": notes, "created_at": created})
	}
	writeJSON(w, 200, out)
}

func (s *server) createCampaign(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		ReleaseID string   `json:"release_id"`
		Name      string   `json:"name"`
		Stages    []int    `json:"stages"`
		Serials   []string `json:"gateway_serials"`
		Threshold int      `json:"failure_threshold"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if err := fleet.ValidateStages(in.Stages); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if in.Name == "" || len(in.Name) > 128 || len(in.Serials) == 0 || len(in.Serials) > 5000 {
		http.Error(w, "name and 1-5000 gateway_serials required", 400)
		return
	}
	if in.Threshold < 0 {
		http.Error(w, "failure_threshold must be >= 0", 400)
		return
	}
	var relOK bool
	if err := s.st.Pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM fleet_releases WHERE id=$1 AND tenant_id=$2)`,
		in.ReleaseID, auth.Tenant(r)).Scan(&relOK); err != nil || !relOK {
		http.Error(w, "release not found", 400)
		return
	}
	stagesJSON, _ := json.Marshal(in.Stages)
	serialsJSON, _ := json.Marshal(in.Serials)
	if in.Threshold == 0 {
		in.Threshold = 3
	}
	id := uuid.NewString()
	if _, err := s.st.Pool.Exec(r.Context(),
		`INSERT INTO fleet_campaigns(id,tenant_id,release_id,name,stages,failure_threshold,serials,created_by)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		id, auth.Tenant(r), in.ReleaseID, in.Name, stagesJSON, in.Threshold, serialsJSON, auth.User(r)); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit(r, "fleet.campaign", id, map[string]any{"name": in.Name, "stages": in.Stages})
	writeJSON(w, 201, map[string]any{"id": id})
}

type campaignRow struct {
	ID, ReleaseID, Name, State string
	Stages                     []int
	Serials                    []string
	StageIndex, Threshold      int
}

func (s *server) loadCampaign(r *http.Request, id string) (*campaignRow, error) {
	var c campaignRow
	var stagesB, serialsB []byte
	err := s.st.Pool.QueryRow(r.Context(),
		`SELECT id, release_id, name, state, stages, serials, stage_index, failure_threshold
		 FROM fleet_campaigns WHERE id=$1 AND tenant_id=$2`, id, auth.Tenant(r)).
		Scan(&c.ID, &c.ReleaseID, &c.Name, &c.State, &stagesB, &serialsB, &c.StageIndex, &c.Threshold)
	if err != nil {
		return nil, err
	}
	json.Unmarshal(stagesB, &c.Stages)
	json.Unmarshal(serialsB, &c.Serials)
	return &c, nil
}

// enterStage assigns the cohort for c.Stages[idx] as pending assignments.
func (s *server) enterStage(w http.ResponseWriter, r *http.Request, c *campaignRow, idx int) bool {
	cohort := fleet.CohortForStage(c.ID, c.Serials, c.Stages, idx)
	tx, err := s.st.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return false
	}
	defer tx.Rollback(r.Context())
	for _, serial := range cohort {
		if _, err := tx.Exec(r.Context(),
			`INSERT INTO fleet_assignments(id,campaign_id,tenant_id,gateway_serial,release_id,state,detail)
			 VALUES($1,$2,$3,$4,$5,'pending','awaiting broker delivery (edge slice)')
			 ON CONFLICT (campaign_id,gateway_serial) DO NOTHING`,
			uuid.NewString(), c.ID, auth.Tenant(r), serial, c.ReleaseID); err != nil {
			http.Error(w, err.Error(), 500)
			return false
		}
	}
	if _, err := tx.Exec(r.Context(),
		`UPDATE fleet_campaigns SET stage_index=$1 WHERE id=$2`, idx, c.ID); err != nil {
		http.Error(w, err.Error(), 500)
		return false
	}
	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, err.Error(), 500)
		return false
	}
	s.fanoutStage(r, c, cohort)
	return true
}

// fanoutStage delivers the release manifest to each cohort gateway as a
// RETAINED broker message: a gateway that is offline at fanout time still
// picks up its assignment on reconnect. Successfully published assignments
// move pending -> sent; failures stay pending for the next regenerate/retry.
func (s *server) fanoutStage(r *http.Request, c *campaignRow, cohort []string) {
	var version string
	var sha *string
	if err := s.st.Pool.QueryRow(r.Context(),
		`SELECT version, artifact_sha256 FROM fleet_releases WHERE id=$1`, c.ReleaseID).
		Scan(&version, &sha); err != nil {
		log.Printf("fleet fanout: release %s: %v", c.ReleaseID, err)
		return
	}
	tenant := auth.Tenant(r)
	for _, serial := range cohort {
		var gwID string
		if err := s.st.Pool.QueryRow(r.Context(),
			`SELECT id FROM gateways WHERE tenant_id=$1 AND serial=$2`, tenant, serial).
			Scan(&gwID); err != nil {
			log.Printf("fleet fanout: unknown gateway %s: %v", serial, err)
			continue
		}
		manifest, _ := json.Marshal(map[string]any{
			"campaign_id": c.ID, "release_id": c.ReleaseID, "version": version,
			"artifact_sha256": sha, "gateway_serial": serial,
		})
		topic := fmt.Sprintf("t/%s/g/%s/fleet", tenant, gwID)
		if err := s.publishMQTTRetained(topic, manifest, true); err != nil {
			log.Printf("fleet fanout: publish %s: %v", serial, err)
			continue
		}
		if _, err := s.st.Pool.Exec(r.Context(),
			`UPDATE fleet_assignments SET state='sent', detail='manifest delivered (retained)', updated_at=now()
			 WHERE campaign_id=$1 AND gateway_serial=$2 AND state='pending'`, c.ID, serial); err != nil {
			log.Printf("fleet fanout: mark sent %s: %v", serial, err)
		}
	}
}

func (s *server) setCampaignState(w http.ResponseWriter, r *http.Request, c *campaignRow, to string) bool {
	if err := fleet.Transition(c.State, to); err != nil {
		http.Error(w, err.Error(), 409)
		return false
	}
	if _, err := s.st.Pool.Exec(r.Context(),
		`UPDATE fleet_campaigns SET state=$1 WHERE id=$2`, to, c.ID); err != nil {
		http.Error(w, err.Error(), 500)
		return false
	}
	return true
}

func (s *server) campaignFailures(r *http.Request, id string) int {
	var n int
	s.st.Pool.QueryRow(r.Context(),
		`SELECT count(*) FROM fleet_assignments WHERE campaign_id=$1 AND state='failed'`, id).Scan(&n)
	return n
}

func (s *server) startCampaign(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	c, err := s.loadCampaign(r, r.PathValue("id"))
	if err != nil {
		http.Error(w, "campaign not found", 404)
		return
	}
	if !s.setCampaignState(w, r, c, fleet.Running) {
		return
	}
	if !s.enterStage(w, r, c, 0) {
		return
	}
	s.audit(r, "fleet.start", c.ID, map[string]any{"stage": 0})
	writeJSON(w, 200, map[string]any{"state": fleet.Running, "stage": 0})
}

func (s *server) advanceCampaign(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	c, err := s.loadCampaign(r, r.PathValue("id"))
	if err != nil {
		http.Error(w, "campaign not found", 404)
		return
	}
	if c.State != fleet.Running {
		http.Error(w, "campaign not running", 409)
		return
	}
	failures := s.campaignFailures(r, c.ID)
	if fleet.Halted(failures, c.Threshold) {
		http.Error(w, "failure threshold reached; campaign halted - rollback or abort", 409)
		return
	}
	// current stage must be fully accounted for before advancing
	var open int
	s.st.Pool.QueryRow(r.Context(),
		`SELECT count(*) FROM fleet_assignments WHERE campaign_id=$1 AND state IN ('pending','sent')`, c.ID).Scan(&open)
	if open > 0 {
		http.Error(w, "current stage still has gateways in flight", 409)
		return
	}
	next := c.StageIndex + 1
	if next >= len(c.Stages) {
		if !s.setCampaignState(w, r, c, fleet.Done) {
			return
		}
		s.audit(r, "fleet.done", c.ID, nil)
		writeJSON(w, 200, map[string]any{"state": fleet.Done})
		return
	}
	if !s.enterStage(w, r, c, next) {
		return
	}
	s.audit(r, "fleet.advance", c.ID, map[string]any{"stage": next})
	writeJSON(w, 200, map[string]any{"state": fleet.Running, "stage": next})
}

func (s *server) pauseCampaign(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	c, err := s.loadCampaign(r, r.PathValue("id"))
	if err != nil {
		http.Error(w, "campaign not found", 404)
		return
	}
	if !s.setCampaignState(w, r, c, fleet.Paused) {
		return
	}
	s.audit(r, "fleet.pause", c.ID, nil)
	writeJSON(w, 200, map[string]any{"state": fleet.Paused})
}

func (s *server) abortCampaign(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	c, err := s.loadCampaign(r, r.PathValue("id"))
	if err != nil {
		http.Error(w, "campaign not found", 404)
		return
	}
	if !s.setCampaignState(w, r, c, fleet.Aborted) {
		return
	}
	s.audit(r, "fleet.abort", c.ID, nil)
	writeJSON(w, 200, map[string]any{"state": fleet.Aborted})
}

// rollbackCampaign creates a single-stage campaign moving every gateway that
// took the rolled-back release onto the chosen good release.
func (s *server) rollbackCampaign(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	c, err := s.loadCampaign(r, r.PathValue("id"))
	if err != nil {
		http.Error(w, "campaign not found", 404)
		return
	}
	var in struct {
		ToReleaseID string `json:"to_release_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.ToReleaseID == "" {
		http.Error(w, "to_release_id required", 400)
		return
	}
	var relOK bool
	if err := s.st.Pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM fleet_releases WHERE id=$1 AND tenant_id=$2)`,
		in.ToReleaseID, auth.Tenant(r)).Scan(&relOK); err != nil || !relOK {
		http.Error(w, "target release not found", 400)
		return
	}
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT gateway_serial FROM fleet_assignments WHERE campaign_id=$1 AND state IN ('acked','failed','sent')`, c.ID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	var serials []string
	for rows.Next() {
		var s2 string
		rows.Scan(&s2)
		serials = append(serials, s2)
	}
	rows.Close()
	if len(serials) == 0 {
		http.Error(w, "no gateways to roll back", 409)
		return
	}
	serialsJSON, _ := json.Marshal(serials)
	rid := uuid.NewString()
	tx, err := s.st.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(),
		`INSERT INTO fleet_campaigns(id,tenant_id,release_id,name,stages,serials,state,stage_index,created_by)
		 VALUES($1,$2,$3,$4,'[100]',$5,'running',0,$6)`,
		rid, auth.Tenant(r), in.ToReleaseID, "rollback of "+c.Name, serialsJSON, auth.User(r)); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	for _, serial := range serials {
		if _, err := tx.Exec(r.Context(),
			`INSERT INTO fleet_assignments(id,campaign_id,tenant_id,gateway_serial,release_id,state,detail)
			 VALUES($1,$2,$3,$4,$5,'pending','rollback target')`,
			uuid.NewString(), rid, auth.Tenant(r), serial, in.ToReleaseID); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if _, err := tx.Exec(r.Context(),
			`UPDATE fleet_assignments SET state='rolled_back', updated_at=now()
			 WHERE campaign_id=$1 AND gateway_serial=$2`, c.ID, serial); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit(r, "fleet.rollback", c.ID, map[string]any{"to_release": in.ToReleaseID, "gateways": len(serials), "rollback_campaign": rid})
	writeJSON(w, 200, map[string]any{"rollback_campaign": rid, "gateways": len(serials)})
}

func (s *server) listCampaigns(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT c.id, c.name, c.state, c.stages, c.stage_index, c.created_at,
		  (SELECT count(*) FROM fleet_assignments a WHERE a.campaign_id=c.id AND a.state='acked') acked,
		  (SELECT count(*) FROM fleet_assignments a WHERE a.campaign_id=c.id AND a.state='failed') failed,
		  (SELECT count(*) FROM fleet_assignments a WHERE a.campaign_id=c.id AND a.state IN ('pending','sent')) open
		 FROM fleet_campaigns c WHERE c.tenant_id=$1 ORDER BY c.created_at DESC`, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, state string
		var stagesB []byte
		var idx, acked, failed, open int
		var created time.Time
		rows.Scan(&id, &name, &state, &stagesB, &idx, &created, &acked, &failed, &open)
		out = append(out, map[string]any{"id": id, "name": name, "state": state,
			"stages": json.RawMessage(stagesB), "stage_index": idx, "created_at": created,
			"acked": acked, "failed": failed, "open": open})
	}
	writeJSON(w, 200, out)
}

// ackAssignment lets an operator (or the edge ACK bridge, when the edge
// slice lands) record a gateway's result for its current campaign.
func (s *server) ackAssignment(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		CampaignID string `json:"campaign_id"`
		Serial     string `json:"gateway_serial"`
		State      string `json:"state"` // acked|failed
		Detail     string `json:"detail"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if in.State != "acked" && in.State != "failed" {
		http.Error(w, "state must be acked or failed", 400)
		return
	}
	res, err := s.st.Pool.Exec(r.Context(),
		`UPDATE fleet_assignments SET state=$1, detail=$2, updated_at=now()
		 WHERE campaign_id=$3 AND gateway_serial=$4 AND tenant_id=$5 AND state IN ('pending','sent')`,
		in.State, in.Detail, in.CampaignID, in.Serial, auth.Tenant(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if res.RowsAffected() == 0 {
		http.Error(w, "no open assignment for that gateway", 404)
		return
	}
	writeJSON(w, 200, map[string]any{"recorded": in.State})
}
