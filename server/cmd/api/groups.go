package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// ---- device groups ----

const maxGroupDevices = 1000

func (s *server) listGroups(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.Pool.Query(r.Context(), `SELECT g.id, g.name, g.description, count(m.device_id) FROM device_groups g
		LEFT JOIN device_group_members m ON m.group_id=g.id WHERE g.tenant_id=$1 GROUP BY g.id ORDER BY g.name LIMIT 500`, auth.Tenant(r))
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, desc string
		var n int
		rows.Scan(&id, &name, &desc, &n)
		out = append(out, map[string]any{"id": id, "name": name, "description": desc, "devices": n})
	}
	writeJSON(w, 200, out)
}

func (s *server) getGroup(w http.ResponseWriter, r *http.Request) {
	var name, desc string
	if s.st.Pool.QueryRow(r.Context(), `SELECT name, description FROM device_groups WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), auth.Tenant(r)).Scan(&name, &desc) != nil {
		http.Error(w, "not found", 404)
		return
	}
	rows, _ := s.st.Pool.Query(r.Context(), `SELECT device_id FROM device_group_members WHERE group_id=$1 AND tenant_id=$2 ORDER BY device_id`, r.PathValue("id"), auth.Tenant(r))
	ids := []string{}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var d string
			rows.Scan(&d)
			ids = append(ids, d)
		}
	}
	writeJSON(w, 200, map[string]any{"id": r.PathValue("id"), "name": name, "description": desc, "device_ids": ids})
}

func (s *server) createGroup(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct{ Name, Description string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	in.Name, in.Description = strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)
	if in.Name == "" || len(in.Name) > 80 || len(in.Description) > 300 {
		http.Error(w, "name required (up to 80 characters); description up to 300", 400)
		return
	}
	id := uuid.NewString()
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO device_groups(id,tenant_id,name,description,created_by) VALUES($1,$2,$3,$4,$5)`, id, auth.Tenant(r), in.Name, in.Description, auth.User(r)); err != nil {
		http.Error(w, "a group with that name already exists", 409)
		return
	}
	s.audit(r, "group.create", id, map[string]any{"name": in.Name})
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *server) deleteGroup(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	tag, err := s.st.Pool.Exec(r.Context(), `DELETE FROM device_groups WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), auth.Tenant(r))
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "group.delete", r.PathValue("id"), nil)
	w.WriteHeader(204)
}

// PUT /v1/groups/{id}/devices {device_ids}: replace the membership. Unknown devices are refused.
func (s *server) setGroupDevices(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		DeviceIDs []string `json:"device_ids"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&in); err != nil || len(in.DeviceIDs) > maxGroupDevices {
		http.Error(w, fmt.Sprintf("send device_ids (up to %d)", maxGroupDevices), 400)
		return
	}
	tenant, gid := auth.Tenant(r), r.PathValue("id")
	var one int
	if s.st.Pool.QueryRow(r.Context(), `SELECT 1 FROM device_groups WHERE id=$1 AND tenant_id=$2`, gid, tenant).Scan(&one) != nil {
		http.Error(w, "not found", 404)
		return
	}
	uniq := map[string]bool{}
	ids := []string{}
	for _, d := range in.DeviceIDs {
		if !uniq[d] {
			uniq[d] = true
			ids = append(ids, d)
		}
	}
	var known int
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM devices WHERE tenant_id=$1 AND id = ANY($2)`, tenant, ids).Scan(&known)
	if known != len(ids) {
		http.Error(w, "some devices do not exist in this workspace", 400)
		return
	}
	tx, err := s.st.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer tx.Rollback(r.Context())
	tx.Exec(r.Context(), `DELETE FROM device_group_members WHERE group_id=$1`, gid)
	if len(ids) > 0 {
		if _, err := tx.Exec(r.Context(), `INSERT INTO device_group_members(group_id,tenant_id,device_id) SELECT $1,$2,unnest($3::text[])`, gid, tenant, ids); err != nil {
			http.Error(w, "db", 500)
			return
		}
	}
	if tx.Commit(r.Context()) != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "group.members", gid, map[string]any{"devices": len(ids)})
	writeJSON(w, 200, map[string]any{"id": gid, "devices": len(ids)})
}

// ---- typed attribute definitions ----

type attrDef struct {
	Key         string   `json:"key"`
	Type        string   `json:"type"` // string | number | boolean | enum
	EnumValues  []string `json:"enum_values"`
	Unit        string   `json:"unit"`
	Description string   `json:"description"`
	Required    bool     `json:"required"`
}

var defKeyRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,39}$`)

func (s *server) loadAttrDefs(ctx context.Context, tenant string) map[string]attrDef {
	out := map[string]attrDef{}
	rows, err := s.st.Pool.Query(ctx, `SELECT key,type,enum_values,unit,description,required FROM attribute_defs WHERE tenant_id=$1`, tenant)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var d attrDef
		rows.Scan(&d.Key, &d.Type, &d.EnumValues, &d.Unit, &d.Description, &d.Required)
		out[d.Key] = d
	}
	return out
}

// checkAttrDefs holds attributes to the tenant's definitions: a defined key must have the defined
// type (and an allowed value for enums), and every required key must be present. Keys without a
// definition stay free-form.
func checkAttrDefs(defs map[string]attrDef, a map[string]any) error {
	keys := make([]string, 0, len(defs))
	for k := range defs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		d := defs[k]
		v, have := a[k]
		if !have {
			if d.Required {
				return fmt.Errorf("attribute %q is required", k)
			}
			continue
		}
		switch d.Type {
		case "number":
			if _, ok := v.(float64); !ok {
				return fmt.Errorf("attribute %q must be a number", k)
			}
		case "boolean":
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("attribute %q must be true or false", k)
			}
		case "string":
			if _, ok := v.(string); !ok {
				return fmt.Errorf("attribute %q must be text", k)
			}
		case "enum":
			sv, ok := v.(string)
			allowed := false
			for _, e := range d.EnumValues {
				allowed = allowed || e == sv
			}
			if !ok || !allowed {
				return fmt.Errorf("attribute %q must be one of: %s", k, strings.Join(d.EnumValues, ", "))
			}
		}
	}
	return nil
}

func (s *server) listAttrDefs(w http.ResponseWriter, r *http.Request) {
	defs := s.loadAttrDefs(r.Context(), auth.Tenant(r))
	out := make([]attrDef, 0, len(defs))
	for _, d := range defs {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	writeJSON(w, 200, out)
}

func (s *server) putAttrDef(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	var d attrDef
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&d); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	d.Key = r.PathValue("key")
	if !defKeyRe.MatchString(d.Key) || len(d.Unit) > 20 || len(d.Description) > 200 {
		http.Error(w, "bad key, unit or description", 400)
		return
	}
	switch d.Type {
	case "string", "number", "boolean":
		d.EnumValues = []string{}
	case "enum":
		if len(d.EnumValues) < 1 || len(d.EnumValues) > 50 {
			http.Error(w, "an enum needs 1-50 allowed values", 400)
			return
		}
		for _, e := range d.EnumValues {
			if e == "" || len(e) > 100 {
				http.Error(w, "enum values must be 1-100 characters", 400)
				return
			}
		}
	default:
		http.Error(w, "type must be string, number, boolean or enum", 400)
		return
	}
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO attribute_defs(tenant_id,key,type,enum_values,unit,description,required) VALUES($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (tenant_id,key) DO UPDATE SET type=EXCLUDED.type, enum_values=EXCLUDED.enum_values, unit=EXCLUDED.unit, description=EXCLUDED.description, required=EXCLUDED.required`,
		auth.Tenant(r), d.Key, d.Type, d.EnumValues, d.Unit, d.Description, d.Required); err != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "attribute_def.put", d.Key, map[string]any{"type": d.Type, "required": d.Required})
	writeJSON(w, 200, d)
}

func (s *server) deleteAttrDef(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	tag, err := s.st.Pool.Exec(r.Context(), `DELETE FROM attribute_defs WHERE tenant_id=$1 AND key=$2`, auth.Tenant(r), r.PathValue("key"))
	if err != nil || tag.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "attribute_def.delete", r.PathValue("key"), nil)
	w.WriteHeader(204)
}
