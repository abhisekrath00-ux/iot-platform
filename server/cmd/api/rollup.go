package main

import (
	"net/http"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// GET /v1/telemetry/rollup?device_id=&point_id=&from=&to=&bucket=hour|day returns
// aggregates (hourly by default; bucket=day merges the hourly rows per UTC day) (n, avg, min, max). Unlike raw series it still answers after raw
// retention has purged old samples. Defaults to the last 30 days; max span 2 years.
func (s *server) rollupTelemetry(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	to := time.Now()
	from := to.Add(-30 * 24 * time.Hour)
	var err error
	if v := q.Get("from"); v != "" {
		if from, err = time.Parse(time.RFC3339, v); err != nil {
			http.Error(w, "from must be RFC3339", 400)
			return
		}
	}
	if v := q.Get("to"); v != "" {
		if to, err = time.Parse(time.RFC3339, v); err != nil {
			http.Error(w, "to must be RFC3339", 400)
			return
		}
	}
	if !to.After(from) || to.Sub(from) > 2*366*24*time.Hour {
		http.Error(w, "range must be positive and at most 2 years", 400)
		return
	}
	if q.Get("device_id") == "" || q.Get("point_id") == "" {
		http.Error(w, "device_id and point_id required", 400)
		return
	}
	unit := "hour"
	switch q.Get("bucket") {
	case "", "hour":
	case "day":
		unit = "day"
	default:
		http.Error(w, "bucket must be hour or day", 400)
		return
	}
	// Daily buckets are merged from hourly rows (sum of n and sum, min of min,
	// max of max), so avg stays exact. Days are UTC.
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT date_trunc($6, bucket AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' AS b, sum(n)::bigint, sum(sum)/sum(n), min(min), max(max)
		 FROM telemetry_rollup_hourly
		 WHERE tenant_id=$1 AND device_id=$2 AND point_id=$3 AND bucket >= $4 AND bucket < $5
		 GROUP BY 1 ORDER BY 1 LIMIT 20000`, auth.Tenant(r), q.Get("device_id"), q.Get("point_id"), from, to, unit)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var b time.Time
		var n int64
		var avg, mn, mx float64
		rows.Scan(&b, &n, &avg, &mn, &mx)
		out = append(out, map[string]any{"t": b, "n": n, "avg": avg, "min": mn, "max": mx})
	}
	writeJSON(w, 200, out)
}
