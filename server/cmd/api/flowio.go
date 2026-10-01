package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/flow"
	"github.com/google/uuid"
)

const flowExportFormat = "hexmon-flow/1"

type flowFile struct {
	Format     string          `json:"format"`
	Name       string          `json:"name"`
	Definition flow.Definition `json:"definition"`
	// Channels maps a notify step's index to a portable "type:target" label,
	// because channel ids are tenant-specific.
	Channels map[int]string `json:"channels,omitempty"`
}

// exportFlow returns the published definition as a portable file. Notify
// channel ids are replaced by "type:target" labels so the file can move
// between tenants and installs (including air-gapped ones, by file copy).
func (s *server) exportFlow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var name string
	var def []byte
	err := s.st.Pool.QueryRow(r.Context(),
		`SELECT f.name, v.definition FROM flows f JOIN flow_versions v ON v.id = f.published_version_id
		 WHERE f.id=$1 AND f.tenant_id=$2`, id, auth.Tenant(r)).Scan(&name, &def)
	if err != nil {
		http.Error(w, "flow not found or not published", 404)
		return
	}
	out := flowFile{Format: flowExportFormat, Name: name, Channels: map[int]string{}}
	if json.Unmarshal(def, &out.Definition) != nil {
		http.Error(w, "stored definition invalid", 500)
		return
	}
	for i, slot := range out.Definition.ChannelSlots() {
		var typ, target string
		if s.st.Pool.QueryRow(r.Context(), `SELECT type,target FROM notification_channels WHERE id=$1 AND tenant_id=$2`,
			*slot, auth.Tenant(r)).Scan(&typ, &target) == nil {
			out.Channels[i] = typ + ":" + target
		}
		*slot = ""
	}
	w.Header().Set("Content-Disposition", `attachment; filename="flow.hexmon.json"`)
	writeJSON(w, 200, out)
}

// importFlow creates a flow from an exported file as an UNPUBLISHED draft: an
// imported file never starts notifying until a person reviews and publishes it.
// Channel labels must resolve to an existing channel of this tenant.
func (s *server) importFlow(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	var in flowFile
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Format != flowExportFormat {
		http.Error(w, "not a "+flowExportFormat+" file", 400)
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 128 || strings.ContainsAny(name, "<>\x00") {
		http.Error(w, "name invalid", 400)
		return
	}
	if !s.gateFunctionNodes(w, r, in.Definition) {
		return
	}
	for i, slot := range in.Definition.ChannelSlots() {
		label, ok := in.Channels[i]
		typ, target, found := strings.Cut(label, ":")
		if !ok || !found {
			http.Error(w, "notify step has no channel label", 400)
			return
		}
		if err := s.st.Pool.QueryRow(r.Context(),
			`SELECT id FROM notification_channels WHERE tenant_id=$1 AND type=$2 AND target=$3 ORDER BY created_at LIMIT 1`,
			auth.Tenant(r), typ, target).Scan(slot); err != nil {
			http.Error(w, "no channel "+label+" in this tenant; create it first", 400)
			return
		}
	}
	if err := flow.Validate(in.Definition); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	def, _ := json.Marshal(in.Definition)
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
	s.audit(r, "flow.import", id, map[string]any{"name": name})
	writeJSON(w, 201, map[string]any{"id": id, "version": 1, "status": "draft"})
}
