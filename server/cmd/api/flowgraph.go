package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow/jsfn"
	"github.com/google/uuid"
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
	if d.HasHTTPNodes() && !(auth.Role(r) == "admin" && s.featureEnabled(r, "http_nodes")) {
		http.Error(w, "http nodes require an admin and the http_nodes feature enabled for this tenant", 403)
		return false
	}
	if d.HasControlNodes() && !(auth.Role(r) == "admin" && s.featureEnabled(r, "control_nodes")) {
		http.Error(w, "control nodes require an admin and the control_nodes feature enabled for this tenant", 403)
		return false
	}
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
	writeJSON(w, 200, map[string]any{"function_nodes": s.featureEnabled(r, "function_nodes"), "http_nodes": s.featureEnabled(r, "http_nodes"), "control_nodes": s.featureEnabled(r, "control_nodes"), featureTOTP: s.featureEnabled(r, featureTOTP)})
}

func (s *server) putFeature(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	feature := r.PathValue("feature")
	if feature != "function_nodes" && feature != "http_nodes" && feature != "control_nodes" && feature != featureTOTP {
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
	opt.Scheduled = in.Definition.IsScheduled() // a test of a timed flow runs it as if the timer fired
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

// exportNodeRED writes the published flow as a Node-RED flow array. Notify
// channels are written as portable "type:target" labels, as in the native export.
func (s *server) exportNodeRED(w http.ResponseWriter, r *http.Request) {
	var name string
	var def []byte
	if s.st.Pool.QueryRow(r.Context(),
		`SELECT f.name, v.definition FROM flows f JOIN flow_versions v ON v.id = f.published_version_id
		 WHERE f.id=$1 AND f.tenant_id=$2`, r.PathValue("id"), auth.Tenant(r)).Scan(&name, &def) != nil {
		http.Error(w, "flow not found or not published", 404)
		return
	}
	var d flow.Definition
	if json.Unmarshal(def, &d) != nil {
		http.Error(w, "stored definition invalid", 500)
		return
	}
	g := flow.ToGraph(d)
	d.Graph = &g
	for _, slot := range d.ChannelSlots() {
		var typ, target string
		if s.st.Pool.QueryRow(r.Context(), `SELECT type,target FROM notification_channels WHERE id=$1 AND tenant_id=$2`,
			*slot, auth.Tenant(r)).Scan(&typ, &target) == nil {
			*slot = typ + ":" + target
		} else {
			*slot = ""
		}
	}
	w.Header().Set("Content-Disposition", `attachment; filename="flow.node-red.json"`)
	writeJSON(w, 200, flow.ToNodeRED(g, name))
}

// importNodeRED reads a Node-RED flow array (supported subset only) into an
// UNPUBLISHED draft. Unsupported nodes refuse the whole import and are listed.
func (s *server) importNodeRED(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		Name  string           `json:"name"`
		Flows []map[string]any `json:"flows"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10)).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 128 || strings.ContainsAny(name, "<>\x00") {
		http.Error(w, "name invalid", 400)
		return
	}
	g, unsupported, err := flow.FromNodeRED(in.Flows)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error(), "unsupported": unsupported})
		return
	}
	d := flow.Definition{Graph: &g}
	for _, slot := range d.ChannelSlots() {
		typ, target, ok := strings.Cut(*slot, ":")
		if !ok || s.st.Pool.QueryRow(r.Context(),
			`SELECT id FROM notification_channels WHERE tenant_id=$1 AND type=$2 AND target=$3 ORDER BY created_at LIMIT 1`,
			auth.Tenant(r), typ, target).Scan(slot) != nil {
			http.Error(w, "notify node needs channel_id \"type:target\" matching an existing channel of this tenant", 400)
			return
		}
	}
	if err := flow.Validate(d); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if !s.gateFunctionNodes(w, r, d) {
		return
	}
	def, _ := json.Marshal(d)
	id, verID := uuid.NewString(), uuid.NewString()
	tx, err := s.st.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(), `INSERT INTO flows(id,tenant_id,name,definition,created_by) VALUES($1,$2,$3,$4,$5)`,
		id, auth.Tenant(r), name, def, auth.User(r)); err != nil {
		http.Error(w, "db", 500)
		return
	}
	if _, err := tx.Exec(r.Context(), `INSERT INTO flow_versions(id,flow_id,tenant_id,version,definition,status,created_by)
		VALUES($1,$2,$3,1,$4,'draft',$5)`, verID, id, auth.Tenant(r), def, auth.User(r)); err != nil {
		http.Error(w, "db", 500)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "flow.import_nodered", id, map[string]any{"name": name})
	writeJSON(w, 201, map[string]any{"id": id, "version": 1, "status": "draft"})
}
