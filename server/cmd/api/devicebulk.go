package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/google/uuid"
)

type profTemplate struct {
	Driver string
	Points []byte
}

const (
	bulkMaxRows  = 500
	bulkMaxBytes = 1 << 20
)

type bulkRow struct {
	Line      int
	GatewayID string
	Profile   string
	Name      string
	Tags      []string
	AssetID   string
}

// parseBulkCSV reads header-driven CSV: gateway_id, profile_id, name are required
// columns; tags (semicolon separated) and asset_id are optional. Every problem
// is collected with its line number so the user can fix the file in one pass.
func parseBulkCSV(r io.Reader) (rows []bulkRow, problems []string) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	head, err := cr.Read()
	if err != nil {
		return nil, []string{"line 1: cannot read header"}
	}
	col := map[string]int{}
	for i, h := range head {
		col[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))] = i
	}
	for _, need := range []string{"gateway_id", "profile_id", "name"} {
		if _, ok := col[need]; !ok {
			problems = append(problems, "header must include column "+need)
		}
	}
	if len(problems) > 0 {
		return nil, problems
	}
	get := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	line := 1
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		line++
		if err != nil {
			problems = append(problems, fmt.Sprintf("line %d: %v", line, err))
			continue
		}
		if len(rows) >= bulkMaxRows {
			problems = append(problems, fmt.Sprintf("more than %d rows; split the file", bulkMaxRows))
			break
		}
		br := bulkRow{Line: line, GatewayID: get(rec, "gateway_id"), Profile: get(rec, "profile_id"), Name: get(rec, "name"), AssetID: get(rec, "asset_id")}
		if br.GatewayID == "" || br.Profile == "" || br.Name == "" {
			problems = append(problems, fmt.Sprintf("line %d: gateway_id, profile_id and name are required", line))
			continue
		}
		if len(br.Name) > 128 || len(br.Profile) > 64 {
			problems = append(problems, fmt.Sprintf("line %d: name over 128 or profile_id over 64 characters", line))
			continue
		}
		if t := get(rec, "tags"); t != "" {
			tags, ok := normalizeTags(strings.Split(t, ";"))
			if !ok {
				problems = append(problems, fmt.Sprintf("line %d: tags must be 1-32 chars of a-z 0-9 . _ : - separated by ;", line))
				continue
			}
			br.Tags = tags
		}
		rows = append(rows, br)
	}
	if len(rows) == 0 && len(problems) == 0 {
		problems = append(problems, "no data rows")
	}
	return rows, problems
}

// bulkCreateDevices: POST /v1/devices/bulk with a CSV body. All or nothing:
// any problem (bad row, unknown gateway, profile or asset of another tenant)
// rejects the whole file with every problem listed and nothing is created.
// ?dry_run=1 validates only.
func (s *server) bulkCreateDevices(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "installer") {
		return
	}
	rows, problems := parseBulkCSV(http.MaxBytesReader(w, r.Body, bulkMaxBytes))
	tenant := auth.Tenant(r)
	var profs map[string]profTemplate
	if len(problems) == 0 {
		gws, assets := map[string]bool{}, map[string]bool{}
		load := func(q string, into map[string]bool) {
			rs, err := s.st.Pool.Query(r.Context(), q, tenant)
			if err != nil {
				return
			}
			defer rs.Close()
			for rs.Next() {
				var id string
				if rs.Scan(&id) == nil {
					into[id] = true
				}
			}
		}
		load(`SELECT id FROM gateways WHERE tenant_id=$1`, gws)
		load(`SELECT id FROM assets WHERE tenant_id=$1`, assets)
		profs = map[string]profTemplate{}
		if rs, err := s.st.Pool.Query(r.Context(), `SELECT id, driver_profile, points FROM device_profiles WHERE tenant_id=$1`, tenant); err == nil {
			for rs.Next() {
				var id string
				var pt profTemplate
				if rs.Scan(&id, &pt.Driver, &pt.Points) == nil {
					profs[id] = pt
				}
			}
			rs.Close()
		}
		for _, b := range rows {
			if !gws[b.GatewayID] {
				problems = append(problems, fmt.Sprintf("line %d: unknown gateway %q", b.Line, b.GatewayID))
			}
			if b.AssetID != "" && !assets[b.AssetID] {
				problems = append(problems, fmt.Sprintf("line %d: unknown asset %q", b.Line, b.AssetID))
			}
			if _, ok := profs[b.Profile]; !ok {
				problems = append(problems, fmt.Sprintf("line %d: unknown profile_id %q", b.Line, b.Profile))
			}
		}
	}
	if len(problems) > 0 {
		if len(problems) > 50 {
			problems = append(problems[:50], fmt.Sprintf("... and %d more", len(problems)-50))
		}
		writeJSON(w, 400, map[string]any{"created": 0, "problems": problems})
		return
	}
	if !s.quotaOK(w, r, "devices", len(rows)) {
		return
	}
	if r.URL.Query().Get("dry_run") == "1" {
		writeJSON(w, 200, map[string]any{"created": 0, "would_create": len(rows), "problems": []string{}})
		return
	}
	tx, err := s.st.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer tx.Rollback(r.Context())
	ids := make([]string, 0, len(rows))
	for _, b := range rows {
		id := uuid.NewString()
		var asset any
		if b.AssetID != "" {
			asset = b.AssetID
		}
		tags := b.Tags
		if tags == nil {
			tags = []string{}
		}
		pt := profs[b.Profile]
		cfg, _ := json.Marshal(map[string]any{"device_profile_id": b.Profile})
		if _, err := tx.Exec(r.Context(),
			`INSERT INTO devices(id,tenant_id,gateway_id,profile,name,config,tags,asset_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
			id, tenant, b.GatewayID, pt.Driver, b.Name, cfg, tags, asset); err != nil {
			http.Error(w, fmt.Sprintf("line %d: %v", b.Line, err), 500)
			return
		}
		var pts []struct {
			ID   string  `json:"id"`
			Unit string  `json:"unit"`
			Min  float64 `json:"min"`
			Max  float64 `json:"max"`
		}
		if json.Unmarshal(pt.Points, &pts) != nil {
			http.Error(w, fmt.Sprintf("line %d: profile points corrupt", b.Line), 500)
			return
		}
		for _, p := range pts {
			if _, err := tx.Exec(r.Context(),
				`INSERT INTO points(id,device_id,unit,min_value,max_value) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`,
				p.ID, id, p.Unit, p.Min, p.Max); err != nil {
				http.Error(w, fmt.Sprintf("line %d: %v", b.Line, err), 500)
				return
			}
		}
		ids = append(ids, id)
	}
	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, "commit failed", 500)
		return
	}
	s.audit(r, "device.bulk_create", "", map[string]any{"count": len(ids)})
	writeJSON(w, 201, map[string]any{"created": len(ids), "ids": ids, "problems": []string{}})
}
