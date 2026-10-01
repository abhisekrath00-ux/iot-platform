package main

import (
	"encoding/json"
	"net/http"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow/jsfn"
)

// fnRunner executes function nodes (goja sandbox, see docs/function-nodes.md).
var fnRunner = jsfn.New(4)

func (s *server) featureEnabled(r *http.Request, feature string) bool {
	var on bool
	s.st.Pool.QueryRow(r.Context(), `SELECT enabled FROM tenant_features WHERE tenant_id=$1 AND feature=$2`,
		auth.Tenant(r), feature).Scan(&on)
	return on
}

// functionNodesAllowed: only an admin of a tenant that enabled the feature may
// save or test flows that contain sandboxed JavaScript.
func (s *server) functionNodesAllowed(r *http.Request) bool {
	return auth.Role(r) == "admin" && s.featureEnabled(r, "function_nodes")
}

// gateFunctionNodes rejects (and writes the error) when the definition holds
// function nodes the caller may not save, or code that does not compile.
func (s *server) gateFunctionNodes(w http.ResponseWriter, r *http.Request, d flow.Definition) bool {
	if !d.HasFunctionNodes() {
		return true
	}
	if !s.functionNodesAllowed(r) {
		http.Error(w, "function nodes require an admin and the function_nodes feature enabled for this tenant", 403)
		return false
	}
	for _, n := range d.Graph.Nodes {
		if n.Type == "function" {
			if err := fnRunner.Check(n.Code); err != nil {
				http.Error(w, "function node "+n.ID+": "+err.Error(), 400)
				return false
			}
		}
	}
	return true
}

func (s *server) getFeatures(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"function_nodes": s.featureEnabled(r, "function_nodes")})
}

func (s *server) putFeature(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	feature := r.PathValue("feature")
	if feature != "function_nodes" {
		http.Error(w, "unknown feature", 404)
		return
	}
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if _, err := s.st.Pool.Exec(r.Context(),
		`INSERT INTO tenant_features(tenant_id,feature,enabled,updated_by) VALUES($1,$2,$3,$4)
		 ON CONFLICT (tenant_id,feature) DO UPDATE SET enabled=EXCLUDED.enabled, updated_by=EXCLUDED.updated_by, updated_at=now()`,
		auth.Tenant(r), feature, in.Enabled, auth.User(r)); err != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "feature.set", feature, map[string]any{"enabled": in.Enabled})
	writeJSON(w, 200, map[string]any{feature: in.Enabled})
}

// testFlowGraph dry-runs a definition against one reading: nothing is
// dispatched and nothing is recorded. Used by the editor's Test button.
func (s *server) testFlowGraph(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		Definition flow.Definition `json:"definition"`
		Value      float64         `json:"value"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if err := flow.Validate(in.Definition); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	opt := flow.ExecOptions{}
	if in.Definition.HasFunctionNodes() && s.functionNodesAllowed(r) {
		opt.Functions = fnRunner
	}
	t := in.Definition.Trig()
	res := in.Definition.Exec(in.Value, t.DeviceID, t.PointID, opt)
	acts := []map[string]any{}
	for _, a := range res.Actions {
		acts = append(acts, map[string]any{"channel_id": a.ChannelID, "message": a.Message, "delay_seconds": int(a.Delay.Seconds())})
	}
	if res.Debug == nil {
		res.Debug = []flow.DebugEntry{}
	}
	writeJSON(w, 200, map[string]any{"matched": res.Matched, "actions": acts, "debug": res.Debug})
}

// convertFlow returns the graph form of a legacy definition (stateless).
func (s *server) convertFlow(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Definition flow.Definition `json:"definition"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if err := flow.Validate(in.Definition); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	g := flow.ToGraph(in.Definition)
	writeJSON(w, 200, map[string]any{"graph": g})
}
