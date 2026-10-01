package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/kpi"
	"github.com/google/uuid"
)

const kpiMaxPerTenant = 100
const kpiStaleAfter = 15 * time.Minute

func (s *server) createKPI(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	var in struct{ Name, Expression, Unit string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if !validAssetName(in.Name) || len(in.Unit) > 16 || strings.ContainsAny(in.Unit, "<>\x00") {
		http.Error(w, "name or unit invalid", 400)
		return
	}
	e, err := kpi.Parse(in.Expression)
	if err != nil {
		http.Error(w, "expression: "+err.Error(), 400)
		return
	}
	if len(e.Refs) == 0 {
		http.Error(w, "expression must reference at least one point", 400)
		return
	}
	for _, ref := range e.Refs {
		var ok bool
		if s.st.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM points p JOIN devices d ON d.id=p.device_id WHERE d.tenant_id=$1 AND d.id=$2 AND p.id=$3)`,
			auth.Tenant(r), ref.Device, ref.Point).Scan(&ok) != nil || !ok {
			http.Error(w, "unknown device or point: "+ref.String(), 400)
			return
		}
	}
	var n int
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FROM kpis WHERE tenant_id=$1`, auth.Tenant(r)).Scan(&n)
	if n >= kpiMaxPerTenant {
		http.Error(w, "too many KPIs", 400)
		return
	}
	id := uuid.NewString()
	if _, err := s.st.Pool.Exec(r.Context(), `INSERT INTO kpis(id,tenant_id,name,expression,unit,created_by) VALUES($1,$2,$3,$4,$5,$6)`,
		id, auth.Tenant(r), in.Name, in.Expression, in.Unit, auth.User(r)); err != nil {
		http.Error(w, "db", 500)
		return
	}
	s.audit(r, "kpi.create", id, map[string]any{"name": in.Name})
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *server) deleteKPI(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	ct, err := s.st.Pool.Exec(r.Context(), `DELETE FROM kpis WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), auth.Tenant(r))
	if err != nil || ct.RowsAffected() == 0 {
		http.Error(w, "not found", 404)
		return
	}
	s.audit(r, "kpi.delete", r.PathValue("id"), nil)
	writeJSON(w, 200, map[string]any{"deleted": true})
}

// listKPIs returns each KPI with its current value computed from the latest
// reading of every referenced point (within 24 hours). A KPI is "stale" when
// any input is older than 15 minutes, and has an error and no value when an
// input is missing or the math is undefined.
func (s *server) listKPIs(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.Pool.Query(r.Context(), `SELECT id,name,expression,unit FROM kpis WHERE tenant_id=$1 ORDER BY name LIMIT 200`, auth.Tenant(r))
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	type row struct{ id, name, expr, unit string }
	var list []row
	for rows.Next() {
		var x row
		if rows.Scan(&x.id, &x.name, &x.expr, &x.unit) == nil {
			list = append(list, x)
		}
	}
	rows.Close()
	type latest struct {
		v  float64
		at time.Time
	}
	cache := map[string]latest{}
	fetch := func(ref kpi.Ref) (latest, bool) {
		if l, ok := cache[ref.String()]; ok {
			return l, true
		}
		var l latest
		if s.st.Pool.QueryRow(r.Context(), `SELECT value, observed_at FROM telemetry
			WHERE tenant_id=$1 AND device_id=$2 AND point_id=$3 AND quality='measured' AND observed_at > now() - interval '24 hours'
			ORDER BY observed_at DESC LIMIT 1`, auth.Tenant(r), ref.Device, ref.Point).Scan(&l.v, &l.at) != nil {
			return l, false
		}
		cache[ref.String()] = l
		return l, true
	}
	out := []map[string]any{}
	for _, k := range list {
		item := map[string]any{"id": k.id, "name": k.name, "expression": k.expr, "unit": k.unit, "value": nil, "stale": false}
		e, err := kpi.Parse(k.expr)
		if err != nil {
			item["error"] = "stored expression invalid"
			out = append(out, item)
			continue
		}
		vals := map[string]float64{}
		stale := false
		for _, ref := range e.Refs {
			if l, ok := fetch(ref); ok {
				vals[ref.String()] = l.v
				if time.Since(l.at) > kpiStaleAfter {
					stale = true
				}
			}
		}
		if v, err := e.Eval(vals); err != nil {
			item["error"] = err.Error()
		} else {
			item["value"] = v
			item["stale"] = stale
		}
		out = append(out, item)
	}
	writeJSON(w, 200, out)
}
