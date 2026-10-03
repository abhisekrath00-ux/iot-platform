package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/google/uuid"
)

const maxAssetDepth = 8

func validAssetName(n string) bool {
	return n != "" && len(n) <= 128 && !strings.ContainsAny(n, "<>\x00")
}

// listAssets returns the tenant's assets flat (parent_id links build the tree
// client-side) with the number of devices attached to each.
func (s *server) listAssets(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT a.id, a.parent_id, a.name, a.kind, (SELECT count(*) FROM devices d WHERE d.asset_id=a.id), a.attributes
		 FROM assets a WHERE a.tenant_id=$1 ORDER BY a.name LIMIT 5000`, auth.Tenant(r))
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, kind string
		var parent *string
		var n int
		var attrs map[string]any
		if rows.Scan(&id, &parent, &name, &kind, &n, &attrs) == nil {
			out = append(out, map[string]any{"id": id, "parent_id": parent, "name": name, "kind": kind, "devices": n, "attributes": attrs})
		}
	}
	writeJSON(w, 200, out)
}

// assetDepth returns how many ancestors an asset has (0 for a root) and
// whether it exists for this tenant. Bounded loop, so bad data cannot hang it.
func (s *server) assetDepth(r *http.Request, id string) (int, bool) {
	depth := 0
	cur := id
	for i := 0; i <= maxAssetDepth+1; i++ {
		var parent *string
		if err := s.st.Pool.QueryRow(r.Context(), `SELECT parent_id FROM assets WHERE id=$1 AND tenant_id=$2`, cur, auth.Tenant(r)).Scan(&parent); err != nil {
			return 0, false
		}
		if parent == nil {
			return depth, true
		}
		depth++
		cur = *parent
	}
	return depth, true
}

func (s *server) createAsset(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		Name     string  `json:"name"`
		Kind     string  `json:"kind"`
		ParentID *string `json:"parent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || !validAssetName(strings.TrimSpace(in.Name)) {
		http.Error(w, "name invalid", 400)
		return
	}
	if in.Kind == "" {
		in.Kind = "asset"
	}
	if len(in.Kind) > 32 || strings.ContainsAny(in.Kind, "<>\x00") {
		http.Error(w, "kind invalid", 400)
		return
	}
	if in.ParentID != nil {
		d, ok := s.assetDepth(r, *in.ParentID)
		if !ok {
			http.Error(w, "parent not found", 404)
			return
		}
		if d+1 >= maxAssetDepth {
			http.Error(w, "hierarchy too deep", 400)
			return
		}
	}
	id := uuid.NewString()
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO assets(id,tenant_id,parent_id,name,kind,created_by) VALUES($1,$2,$3,$4,$5,$6)`,
		id, auth.Tenant(r), in.ParentID, strings.TrimSpace(in.Name), in.Kind, auth.User(r)); err != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "asset.create", id, map[string]any{"name": in.Name})
	writeJSON(w, 201, map[string]any{"id": id})
}

// deleteAsset refuses to orphan data: the asset must have no children and no devices.
func (s *server) deleteAsset(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	id := r.PathValue("id")
	var busy bool
	if err := s.st.Pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM assets WHERE parent_id=$1) OR EXISTS(SELECT 1 FROM devices WHERE asset_id=$1)`, id).Scan(&busy); err != nil {
		http.Error(w, "db", 500)
		return
	}
	if busy {
		http.Error(w, "asset has children or devices", 409)
		return
	}
	s.st.Pool.Exec(r.Context(), `DELETE FROM entity_relations WHERE tenant_id=$1 AND ((from_kind='asset' AND from_id=$2) OR (to_kind='asset' AND to_id=$2))`, auth.Tenant(r), id)
	ct, err := s.st.Pool.Exec(r.Context(), `DELETE FROM assets WHERE id=$1 AND tenant_id=$2`, id, auth.Tenant(r))
	if err != nil || ct.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "asset.delete", id, nil)
	writeJSON(w, 200, map[string]any{"deleted": true})
}

// setDeviceAsset attaches a device to an asset (null detaches). Both must belong to the caller's tenant.
func (s *server) setDeviceAsset(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		AssetID *string `json:"asset_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if in.AssetID != nil {
		if _, ok := s.assetDepth(r, *in.AssetID); !ok {
			http.Error(w, "asset not found", 404)
			return
		}
	}
	ct, err := s.st.Pool.Exec(r.Context(), `UPDATE devices SET asset_id=$1 WHERE id=$2 AND tenant_id=$3`, in.AssetID, r.PathValue("id"), auth.Tenant(r))
	if err != nil || ct.RowsAffected() == 0 {
		http.Error(w, "device not found", 404)
		return
	}
	s.audit(r, "device.asset", r.PathValue("id"), map[string]any{"asset_id": in.AssetID})
	writeJSON(w, 200, map[string]any{"ok": true})
}

// PUT /v1/assets/{id}/attributes (admin, operator): replace the asset's attributes. Same rules as
// device attributes (size, key shape, no secrets) plus the tenant's definitions that apply to assets.
func (s *server) setAssetAttributes(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct {
		Attributes map[string]any `json:"attributes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&in); err != nil || in.Attributes == nil {
		http.Error(w, "bad json: expected {\"attributes\": {...}}", 400)
		return
	}
	if err := validateAttributes(in.Attributes); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err := checkAttrDefs(s.loadAttrDefsFor(r.Context(), auth.Tenant(r), "asset"), in.Attributes); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	raw, _ := json.Marshal(in.Attributes)
	id := r.PathValue("id")
	ct, err := s.st.Pool.Exec(r.Context(), `UPDATE assets SET attributes=$1 WHERE id=$2 AND tenant_id=$3`, raw, id, auth.Tenant(r))
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	if ct.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "asset.attributes", id, map[string]any{"count": len(in.Attributes)})
	writeJSON(w, 200, map[string]any{"attributes": in.Attributes})
}
