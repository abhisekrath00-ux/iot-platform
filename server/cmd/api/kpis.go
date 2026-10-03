package main

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strconv"
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

// GET /v1/kpis/{id}/history?hours=24 (1-720): the KPI over time, one point per hour. Each input is
// averaged per hour (from hourly rollups, plus raw readings for hours not yet rolled up), then the
// expression is applied to the hours where every input has data. For a ratio this is the ratio of
// hourly averages, not the average of the ratio. Nothing is stored: it is computed from telemetry.
func (s *server) kpiHistory(w http.ResponseWriter, r *http.Request) {
	hours := 24
	if v := r.URL.Query().Get("hours"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 720 {
			http.Error(w, "hours must be 1-720", 400)
			return
		}
		hours = n
	}
	var name, expr, unit string
	if s.st.Pool.QueryRow(r.Context(), `SELECT name,expression,unit FROM kpis WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), auth.Tenant(r)).Scan(&name, &expr, &unit) != nil {
		http.Error(w, "not found", 404)
		return
	}
	e, err := kpi.Parse(expr)
	if err != nil {
		http.Error(w, "stored expression invalid", 500)
		return
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour).Truncate(time.Hour)
	perBucket := map[time.Time]map[string]float64{}
	for _, ref := range e.Refs {
		rows, err := s.st.Pool.Query(r.Context(), `
			WITH r AS (SELECT bucket b, sum/NULLIF(n,0) v FROM telemetry_rollup_hourly WHERE tenant_id=$1 AND device_id=$2 AND point_id=$3 AND bucket>=$4),
			     t AS (SELECT date_trunc('hour',observed_at) b, avg(value) v FROM telemetry WHERE tenant_id=$1 AND device_id=$2 AND point_id=$3 AND quality='measured' AND observed_at>=$4 GROUP BY 1)
			SELECT b, v FROM r WHERE v IS NOT NULL UNION ALL SELECT b, v FROM t WHERE b NOT IN (SELECT b FROM r)`,
			auth.Tenant(r), ref.Device, ref.Point, since)
		if err != nil {
			http.Error(w, "db", 500)
			return
		}
		for rows.Next() {
			var b time.Time
			var v float64
			if rows.Scan(&b, &v) == nil {
				if perBucket[b] == nil {
					perBucket[b] = map[string]float64{}
				}
				perBucket[b][ref.String()] = v
			}
		}
		rows.Close()
	}
	type pt struct {
		T time.Time `json:"t"`
		V float64   `json:"v"`
	}
	pts := []pt{}
	for b, vals := range perBucket {
		if len(vals) != len(e.Refs) {
			continue
		}
		if v, err := e.Eval(vals); err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) {
			pts = append(pts, pt{b, v})
		}
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].T.Before(pts[j].T) })
	out := map[string]any{"id": r.PathValue("id"), "name": name, "unit": unit, "hours": hours, "points": pts,
		"note": "Hourly averages of each input, then the expression. Hours where any input has no data are left out."}
	if len(pts) > 0 {
		mn, mx, sum := pts[0].V, pts[0].V, 0.0
		for _, p := range pts {
			mn, mx, sum = math.Min(mn, p.V), math.Max(mx, p.V), sum+p.V
		}
		out["min"], out["max"], out["avg"] = mn, mx, sum/float64(len(pts))
	}
	writeJSON(w, 200, out)
}
