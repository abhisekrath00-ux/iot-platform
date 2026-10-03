package main

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/jackc/pgx/v5"
)

const (
	importMaxRows  = 20000
	importMaxBytes = 4 << 20
	importMaxAge   = 5 * 366 * 24 * time.Hour
)

type importRow struct {
	line  int
	point string
	at    time.Time
	value float64
	unit  string
}

// parseImportCSV reads header-driven CSV with columns ts (RFC 3339), point and
// value, plus an optional unit. Every problem is collected with its line number.
func parseImportCSV(r io.Reader, now time.Time) (rows []importRow, problems []string) {
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
	for _, need := range []string{"ts", "point", "value"} {
		if _, ok := col[need]; !ok {
			problems = append(problems, "header must include column "+need)
		}
	}
	if len(problems) > 0 {
		return nil, problems
	}
	get := func(rec []string, n string) string {
		if i, ok := col[n]; ok && i < len(rec) {
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
		if len(rows)+len(problems) >= importMaxRows {
			problems = append(problems, fmt.Sprintf("too many rows (max %d per file)", importMaxRows))
			break
		}
		t, e1 := time.Parse(time.RFC3339, get(rec, "ts"))
		v, e2 := strconv.ParseFloat(get(rec, "value"), 64)
		switch {
		case e1 != nil:
			problems = append(problems, fmt.Sprintf("line %d: ts must be RFC 3339", line))
		case t.After(now.Add(5*time.Minute)) || now.Sub(t) > importMaxAge:
			problems = append(problems, fmt.Sprintf("line %d: ts is in the future or more than 5 years old", line))
		case get(rec, "point") == "":
			problems = append(problems, fmt.Sprintf("line %d: point is empty", line))
		case e2 != nil || math.IsNaN(v) || math.IsInf(v, 0):
			problems = append(problems, fmt.Sprintf("line %d: value is not a finite number", line))
		default:
			rows = append(rows, importRow{line, get(rec, "point"), t.UTC(), v, get(rec, "unit")})
		}
	}
	if len(rows) == 0 && len(problems) == 0 {
		problems = append(problems, "no data rows")
	}
	return rows, problems
}

// POST /v1/telemetry/import?device_id=&dry_run=1 takes a CSV body (ts,point,value[,unit])
// of historical readings for one device. Admin only. All-or-nothing: any bad row
// rejects the file with line numbers. Imported rows are stored like measured
// data (same range checks as live ingest) but never evaluate alert rules or
// flows, because history must not raise alerts about the past. Hourly rollups
// for the touched hours are recomputed, except where an existing rollup holds
// more samples than raw data now does (raw already purged); those are kept.
// Re-uploading the same file is a no-op: ids derive from device, point, time and value.
func (s *server) importTelemetry(w http.ResponseWriter, r *http.Request) {
	if !requireRole(w, r, "admin") {
		return
	}
	tenant, dev := auth.Tenant(r), r.URL.Query().Get("device_id")
	var gw string
	if dev == "" || s.st.Pool.QueryRow(r.Context(), `SELECT gateway_id FROM devices WHERE id=$1 AND tenant_id=$2`, dev, tenant).Scan(&gw) != nil {
		http.Error(w, "unknown device", 404)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, importMaxBytes)
	rows, problems := parseImportCSV(r.Body, time.Now())
	type pt struct {
		unit     string
		min, max *float64
	}
	pts := map[string]pt{}
	pr, err := s.st.Pool.Query(r.Context(), `SELECT id, unit, min_value, max_value FROM points WHERE device_id=$1`, dev)
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	for pr.Next() {
		var id string
		var p pt
		if pr.Scan(&id, &p.unit, &p.min, &p.max) == nil {
			pts[id] = p
		}
	}
	pr.Close()
	for _, rw := range rows {
		p, ok := pts[rw.point]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("line %d: unknown point %q", rw.line, rw.point))
		case (p.min != nil && rw.value < *p.min) || (p.max != nil && rw.value > *p.max):
			problems = append(problems, fmt.Sprintf("line %d: value out of range for %s", rw.line, rw.point))
		}
		if len(problems) >= 50 {
			problems = append(problems, "stopped after 50 problems")
			break
		}
	}
	if len(problems) > 0 {
		writeJSON(w, 422, map[string]any{"imported": 0, "problems": problems})
		return
	}
	if r.URL.Query().Get("dry_run") == "1" {
		writeJSON(w, 200, map[string]any{"dry_run": true, "rows": len(rows)})
		return
	}
	tx, err := s.st.Pool.Begin(r.Context())
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer tx.Rollback(r.Context())
	b := &pgx.Batch{}
	minT, maxT := rows[0].at, rows[0].at
	for _, rw := range rows {
		unit := rw.unit
		if unit == "" {
			unit = pts[rw.point].unit
		}
		h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%d|%v", tenant, dev, rw.point, rw.at.UnixNano(), rw.value)))
		b.Queue(`INSERT INTO telemetry(event_id,tenant_id,site_id,gateway_id,device_id,point_id,observed_at,value,unit,quality,schema_version)
			VALUES($1,$2,NULL,$3,$4,$5,$6,$7,$8,'measured',1) ON CONFLICT (event_id, observed_at) DO NOTHING`,
			"import-"+hex.EncodeToString(h[:12]), tenant, gw, dev, rw.point, rw.at, rw.value, unit)
		if rw.at.Before(minT) {
			minT = rw.at
		}
		if rw.at.After(maxT) {
			maxT = rw.at
		}
	}
	br := tx.SendBatch(r.Context(), b)
	inserted := int64(0)
	for range rows {
		ct, err := br.Exec()
		if err != nil {
			br.Close()
			http.Error(w, "store error", 500)
			return
		}
		inserted += ct.RowsAffected()
	}
	br.Close()
	from, to := minT.Truncate(time.Hour), maxT.Truncate(time.Hour).Add(time.Hour)
	ct, err := tx.Exec(r.Context(), `
		INSERT INTO telemetry_rollup_hourly(tenant_id, device_id, point_id, bucket, n, sum, min, max)
		SELECT tenant_id, device_id, point_id, date_trunc('hour', observed_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC',
		       count(*), sum(value), min(value), max(value)
		FROM telemetry
		WHERE tenant_id=$1 AND device_id=$2 AND observed_at >= $3 AND observed_at < $4 AND quality IN ('measured','estimated')
		GROUP BY 1,2,3,4
		ON CONFLICT (tenant_id, device_id, point_id, bucket)
		DO UPDATE SET n=EXCLUDED.n, sum=EXCLUDED.sum, min=EXCLUDED.min, max=EXCLUDED.max
		WHERE EXCLUDED.n >= telemetry_rollup_hourly.n`, tenant, dev, from, to)
	if err != nil {
		http.Error(w, "rollup error", 500)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, "commit error", 500)
		return
	}
	s.audit(r, "telemetry.import", dev, map[string]any{"rows": len(rows), "inserted": inserted, "from": minT, "to": maxT})
	writeJSON(w, 200, map[string]any{"imported": inserted, "duplicates": int64(len(rows)) - inserted, "rollup_buckets": ct.RowsAffected()})
}
