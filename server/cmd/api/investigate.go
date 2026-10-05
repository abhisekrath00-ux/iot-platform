package main

import (
	"net/http"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/diagnose"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/health"
)

// GET /v1/diagnostics/investigate?scope=North+plant (admin, operator): finds the sites and assets
// whose name matches the scope, collects their devices, gateways and open alerts, and applies the
// fixed rules in internal/diagnose. It reads only, runs no model, and the same data gives the same
// report. The assistant calls it so the counting and the cause analysis are done by code, not by
// the model.
func (s *server) investigate(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin", "operator") {
		return
	}
	tenant := auth.Tenant(r)
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if scope == "" || len(scope) > 80 {
		http.Error(w, "scope is required: a site or asset name, up to 80 characters", 400)
		return
	}
	ctx := r.Context()
	like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(scope) + "%"
	var siteIDs, assetIDs, matched []string
	rows, err := s.st.Pool.Query(ctx, `SELECT 'site', id, name FROM sites WHERE tenant_id=$1 AND name ILIKE $2 ESCAPE '\' UNION ALL
	  SELECT 'asset', id, name FROM assets WHERE tenant_id=$1 AND name ILIKE $2 ESCAPE '\' LIMIT 40`, tenant, like)
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	for rows.Next() {
		var kind, id, name string
		if rows.Scan(&kind, &id, &name) == nil {
			matched = append(matched, kind+": "+name)
			if kind == "site" {
				siteIDs = append(siteIDs, id)
			} else {
				assetIDs = append(assetIDs, id)
			}
		}
	}
	rows.Close()
	if len(matched) == 0 {
		var known []string
		if r2, err := s.st.Pool.Query(ctx, `SELECT name FROM sites WHERE tenant_id=$1 UNION SELECT name FROM assets WHERE tenant_id=$1 ORDER BY 1 LIMIT 15`, tenant); err == nil {
			for r2.Next() {
				var n string
				if r2.Scan(&n) == nil {
					known = append(known, n)
				}
			}
			r2.Close()
		}
		writeJSON(w, 200, map[string]any{"scope": scope, "matched": []string{}, "message": "No site or asset name matches that scope. Nothing was investigated.", "known_names": known})
		return
	}

	devRows, err := s.st.Pool.Query(ctx, `WITH RECURSIVE sub AS (
	    SELECT id FROM assets WHERE tenant_id=$1 AND id = ANY($3)
	    UNION ALL SELECT a.id FROM assets a JOIN sub ON a.parent_id=sub.id WHERE a.tenant_id=$1)
	  SELECT d.id, d.name, d.gateway_id, COALESCE((d.config->'connection'->>'interval_seconds')::float8,0), g.serial, g.status, g.last_seen_at
	  FROM devices d JOIN gateways g ON g.id=d.gateway_id AND g.tenant_id=d.tenant_id
	  WHERE d.tenant_id=$1 AND (d.asset_id IN (SELECT id FROM sub) OR g.site_id = ANY($2)) ORDER BY d.name LIMIT 200`, tenant, siteIDs, assetIDs)
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	type devRow struct {
		id, name, gw, serial, gwStatus string
		interval                       float64
		gwSeen                         *time.Time
	}
	var devs []devRow
	var ids []string
	gws := map[string]*diagnose.Gateway{}
	var gwOrder []string
	for devRows.Next() {
		var d devRow
		if devRows.Scan(&d.id, &d.name, &d.gw, &d.interval, &d.serial, &d.gwStatus, &d.gwSeen) == nil {
			devs = append(devs, d)
			ids = append(ids, d.id)
			if gws[d.gw] == nil {
				gws[d.gw] = &diagnose.Gateway{ID: d.gw, Serial: d.serial, Status: d.gwStatus, LastSeen: d.gwSeen}
				gwOrder = append(gwOrder, d.gw)
			}
		}
	}
	devRows.Close()

	type seen struct {
		last       *time.Time
		n, measure int
	}
	tel := map[string]seen{}
	if len(ids) > 0 {
		tr, err := s.st.Pool.Query(ctx, `SELECT did, max(t.observed_at), count(*), count(*) FILTER (WHERE t.quality='measured')
		  FROM unnest($2::text[]) did CROSS JOIN LATERAL (SELECT observed_at, quality FROM telemetry WHERE tenant_id=$1 AND device_id=did ORDER BY observed_at DESC LIMIT 50) t GROUP BY did`, tenant, ids)
		if err == nil {
			for tr.Next() {
				var id string
				var x seen
				if tr.Scan(&id, &x.last, &x.n, &x.measure) == nil {
					tel[id] = x
				}
			}
			tr.Close()
		}
	}
	now := time.Now()
	recent := func(t *time.Time) bool { return t != nil && now.Sub(*t) < 10*time.Minute }
	in := diagnose.Input{Scope: scope, Now: now}
	for _, d := range devs {
		x := tel[d.id]
		hi := health.Input{LastSeen: x.last, Now: now, Samples: x.n, MeasuredSamples: x.measure, GatewayOnline: d.gwStatus == "active" && (recent(d.gwSeen) || recent(x.last))}
		if d.interval > 0 {
			hi.Interval = time.Duration(d.interval * float64(time.Second))
		}
		res := health.Score(hi)
		in.Devices = append(in.Devices, diagnose.Device{ID: d.id, Name: d.name, GatewayID: d.gw, Health: res.Status, Score: res.Score, LastSeen: x.last})
	}
	for _, id := range gwOrder {
		in.Gateways = append(in.Gateways, *gws[id])
	}
	if len(ids) > 0 {
		ar, err := s.st.Pool.Query(ctx, `SELECT a.id, a.device_id, a.severity, a.message, a.created_at FROM alerts a WHERE a.tenant_id=$1 AND a.status='open' AND a.device_id = ANY($2) ORDER BY a.created_at DESC LIMIT 200`, tenant, ids)
		if err == nil {
			for ar.Next() {
				var a diagnose.Alert
				if ar.Scan(&a.ID, &a.DeviceID, &a.Severity, &a.Message, &a.At) == nil {
					in.Alerts = append(in.Alerts, a)
				}
			}
			ar.Close()
		}
	}
	writeJSON(w, 200, map[string]any{"matched": matched, "truncated": len(devs) == 200, "report": diagnose.Run(in)})
}
