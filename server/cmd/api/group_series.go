package main

import (
	"net/http"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// groupSeriesTelemetry combines per-device hourly means over the trailing
// 24 hours, using current transitive group membership. Raw samples only:
// historical rollups have no units, so cannot safely establish compatibility.
// Missing device-hours are absent, never zero-filled or carried forward.
// Only measured/estimated finite values contribute; stale/missing placeholders
// cannot affect an aggregate or its unit-compatibility check.
func (s *server) groupSeriesTelemetry(w http.ResponseWriter, r *http.Request) {
	group, point, agg := r.URL.Query().Get("group_id"), r.URL.Query().Get("point_id"), r.URL.Query().Get("agg")
	if group == "" || len(group) > 128 || point == "" || len(point) > 128 {
		http.Error(w, "group_id and point_id required (up to 128 chars)", 400)
		return
	}
	format := r.URL.Query().Get("format")
	if format != "" && format != "csv" {
		http.Error(w, "format must be csv or omitted", 400)
		return
	}
	expr := ""
	switch agg {
	case "sum":
		expr = "sum(v)"
	case "avg":
		expr = "avg(v)"
	case "min":
		expr = "min(v)"
	case "max":
		expr = "max(v)"
	default:
		http.Error(w, "agg must be sum, avg, min or max", 400)
		return
	}
	var exists bool
	if err := s.st.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM device_groups WHERE tenant_id=$1 AND id=$2)`, auth.Tenant(r), group).Scan(&exists); err != nil {
		dbErr(w, err)
		return
	}
	if !exists {
		http.Error(w, "unknown group", 404)
		return
	}
	rows, err := s.st.Pool.Query(r.Context(), `WITH RECURSIVE gsub(id) AS (
 SELECT id FROM device_groups WHERE tenant_id=$1 AND id=$2
 UNION ALL SELECT g.id FROM device_groups g JOIN gsub ON g.parent_id=gsub.id WHERE g.tenant_id=$1),
 `+strings.Replace(customerSubtreeCTE, "%s", "NULLIF($3,'')", 1)+`, members AS (
 SELECT DISTINCT m.device_id FROM device_group_members m WHERE m.tenant_id=$1 AND m.group_id IN (SELECT id FROM gsub)
 AND ($3='' OR m.device_id IN (SELECT id FROM devices WHERE tenant_id=$1 AND customer_id IN (SELECT id FROM csub)))),
 raw AS (
 SELECT t.device_id,date_trunc('hour',t.observed_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' AS bucket,t.value,t.unit
 FROM telemetry t JOIN members m ON m.device_id=t.device_id
 WHERE t.tenant_id=$1 AND t.point_id=$4 AND t.observed_at>=now()-interval '24 hours' AND t.observed_at<=now()
 AND t.quality IN ('measured','estimated') AND t.value > '-Infinity'::float8 AND t.value < 'Infinity'::float8),
 per_device AS (SELECT device_id,bucket,avg(value) v,count(*) samples FROM raw GROUP BY device_id,bucket),
 units AS (SELECT count(DISTINCT unit)>1 mixed, min(unit) unit FROM raw),
 combined AS (SELECT bucket,`+expr+` v,count(*) devices,sum(samples)::bigint samples FROM per_device GROUP BY bucket)
 SELECT bucket,CASE WHEN units.mixed THEN NULL ELSE combined.v END,devices,samples,(SELECT count(*) FROM members),COALESCE(units.unit,''),units.mixed
 FROM combined CROSS JOIN units ORDER BY bucket`, auth.Tenant(r), group, scopeOf(r), point)
	if err != nil {
		dbErr(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var at time.Time
		var v *float64
		var devices, samples, members int
		var unit string
		var mixed bool
		if err := rows.Scan(&at, &v, &devices, &samples, &members, &unit, &mixed); err != nil {
			dbErr(w, err)
			return
		}
		if mixed {
			unit = ""
		}
		out = append(out, map[string]any{"t": at, "v": v, "devices": devices, "members": members, "samples": samples, "unit": unit, "units_mixed": mixed})
	}
	if rows.Err() != nil {
		dbErr(w, rows.Err())
		return
	}
	if format == "csv" {
		writeGroupSeriesCSV(w, group, point, agg, out)
		return
	}
	writeJSON(w, 200, out)
}
