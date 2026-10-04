package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// Custom roles. A custom role is a built-in base role (operator, installer or viewer, never admin) minus
// a set of denied capability groups. It can only take access away: every handler still applies its own
// role check against the base role. Enforcement is in activeUser, so it also covers the assistant, which
// runs as the user. API keys carry their own role and are not affected.
//
// A denied entry is "write:<group>" (all non-GET requests to the group) or "read:<group>" (every request).

var capabilityGroups = map[string][]string{
	"control":    {"/v1/commands", "/v1/control-targets"},
	"flows":      {"/v1/flows", "/v1/flow-fragments"},
	"reports":    {"/v1/reports"},
	"dashboards": {"/v1/dashboards"},
	"alerts":     {"/v1/alerts", "/v1/rules", "/v1/escalation", "/v1/oncall", "/v1/maintenance", "/v1/notifications"},
	"devices":    {"/v1/devices", "/v1/gateways", "/v1/groups", "/v1/commissioning", "/v1/direct-devices", "/v1/enrollment", "/v1/profiles"},
	"fleet":      {"/v1/fleet"},
	"assets":     {"/v1/assets", "/v1/relations", "/v1/attribute-defs", "/v1/kpis", "/v1/geofences", "/v1/map"},
	"assistant":  {"/v1/assistant", "/v1/ai", "/v1/ask"},
	"audit":      {"/v1/audit"},
}

func validDenied(list []string) ([]string, bool) {
	seen := map[string]bool{}
	out := []string{}
	for _, d := range list {
		kind, g, ok := strings.Cut(d, ":")
		if !ok || (kind != "read" && kind != "write") || capabilityGroups[g] == nil {
			return nil, false
		}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out, true
}

// deniedByRole reports whether a custom role's denied list blocks this request.
func deniedByRole(denied []string, method, path string) bool {
	write := method != http.MethodGet && method != http.MethodHead
	for _, d := range denied {
		kind, g, _ := strings.Cut(d, ":")
		if kind == "write" && !write {
			continue
		}
		for _, p := range capabilityGroups[g] {
			if path == p || strings.HasPrefix(path, p+"/") {
				return true
			}
		}
	}
	return false
}

func (s *server) listRoles(w http.ResponseWriter, r *http.Request) {
	if !userAdminOnly(w, r) {
		return
	}
	rows, err := s.st.Pool.Query(r.Context(), `SELECT r.id, r.name, r.base_role, r.denied,
		(SELECT count(*) FROM users u WHERE u.custom_role_id=r.id) FROM custom_roles r WHERE r.tenant_id=$1 ORDER BY r.name`, auth.Tenant(r))
	if err != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, base string
		var denied []string
		var n int
		if rows.Scan(&id, &name, &base, &denied, &n) == nil {
			out = append(out, map[string]any{"id": id, "name": name, "base_role": base, "denied": denied, "users": n})
		}
	}
	groups := make([]string, 0, len(capabilityGroups))
	for g := range capabilityGroups {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	writeJSON(w, 200, map[string]any{"roles": out, "groups": groups})
}

type roleIn struct {
	Name     string   `json:"name"`
	BaseRole string   `json:"base_role"`
	Denied   []string `json:"denied"`
}

func (s *server) parseRole(w http.ResponseWriter, r *http.Request) (roleIn, bool) {
	var in roleIn
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in) != nil {
		http.Error(w, "bad json", 400)
		return in, false
	}
	in.Name = strings.TrimSpace(in.Name)
	if !validDisplayName(in.Name) {
		http.Error(w, "name required (up to 100 characters, no markup)", 400)
		return in, false
	}
	if in.BaseRole != "operator" && in.BaseRole != "installer" && in.BaseRole != "viewer" {
		http.Error(w, "base_role must be operator, installer or viewer (a custom role is never admin)", 400)
		return in, false
	}
	d, ok := validDenied(in.Denied)
	if !ok {
		http.Error(w, "denied entries must look like write:<group> or read:<group> for a known group", 400)
		return in, false
	}
	in.Denied = d
	return in, true
}

func (s *server) createRole(w http.ResponseWriter, r *http.Request) {
	if !userAdminOnly(w, r) {
		return
	}
	in, ok := s.parseRole(w, r)
	if !ok {
		return
	}
	id := "role-" + uuid.NewString()
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO custom_roles(id,tenant_id,name,base_role,denied) VALUES($1,$2,$3,$4,$5)`, id, auth.Tenant(r), in.Name, in.BaseRole, in.Denied); err != nil {
		http.Error(w, "a role with that name already exists", 409)
		return
	}
	s.audit(r, "role.create", id, map[string]any{"name": in.Name, "base_role": in.BaseRole, "denied": in.Denied})
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *server) updateRole(w http.ResponseWriter, r *http.Request) {
	if !userAdminOnly(w, r) {
		return
	}
	in, ok := s.parseRole(w, r)
	if !ok {
		return
	}
	tx, err := s.st.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	defer tx.Rollback(r.Context())
	tag, err := tx.Exec(r.Context(), `UPDATE custom_roles SET name=$3, base_role=$4, denied=$5 WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), auth.Tenant(r), in.Name, in.BaseRole, in.Denied)
	if err != nil {
		http.Error(w, "a role with that name already exists", 409)
		return
	}
	if tag.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	// members follow the base role at once
	tx.Exec(r.Context(), `UPDATE users SET role=$3 WHERE custom_role_id=$1 AND tenant_id=$2`, r.PathValue("id"), auth.Tenant(r), in.BaseRole)
	if tx.Commit(r.Context()) != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	s.audit(r, "role.update", r.PathValue("id"), map[string]any{"name": in.Name, "base_role": in.BaseRole, "denied": in.Denied})
	w.WriteHeader(204)
}

func (s *server) deleteRole(w http.ResponseWriter, r *http.Request) {
	if !userAdminOnly(w, r) {
		return
	}
	var n int
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM users WHERE custom_role_id=$1 AND tenant_id=$2`, r.PathValue("id"), auth.Tenant(r)).Scan(&n)
	if n > 0 {
		http.Error(w, "users still have this role; move them first", 409)
		return
	}
	tag, err := s.st.Pool.Exec(r.Context(), `DELETE FROM custom_roles WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), auth.Tenant(r))
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "role.delete", r.PathValue("id"), nil)
	w.WriteHeader(204)
}

// customDenied returns the denied list of the user's custom role (nil if none).
func (s *server) customDenied(r *http.Request) []string {
	var denied []string
	err := s.st.Pool.QueryRow(r.Context(), `SELECT cr.denied FROM users u JOIN custom_roles cr ON cr.id=u.custom_role_id WHERE u.id=$1 AND u.tenant_id=$2`, auth.User(r), auth.Tenant(r)).Scan(&denied)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return []string{"__fail_closed__"}
	}
	return denied
}
