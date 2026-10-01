package main

import (
	"net/http"
	"strconv"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/anomaly"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// anomaliesTelemetry flags outliers in one point's recent readings using a
// robust median/MAD score (see internal/anomaly). Query: device_id, point_id,
// hours (1-168, default 24), threshold (2-10, default 3.5). Read-only; only
// measured-quality samples count.
func (s *server) anomaliesTelemetry(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dev, pt := q.Get("device_id"), q.Get("point_id")
	if dev == "" || pt == "" {
		http.Error(w, "device_id and point_id required", 400)
		return
	}
	hours := 24
	if v := q.Get("hours"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 168 {
			http.Error(w, "hours must be 1-168", 400)
			return
		}
		hours = n
	}
	threshold := 3.5
	if v := q.Get("threshold"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 2 || f > 10 {
			http.Error(w, "threshold must be 2-10", 400)
			return
		}
		threshold = f
	}
	rows, err := s.st.Pool.Query(r.Context(),
		`SELECT observed_at, value FROM telemetry
		 WHERE tenant_id=$1 AND device_id=$2 AND point_id=$3 AND quality='measured'
		   AND observed_at > now() - make_interval(hours => $4)
		 ORDER BY observed_at DESC LIMIT 5000`, auth.Tenant(r), dev, pt, hours)
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer rows.Close()
	var samples []anomaly.Sample
	for rows.Next() {
		var t time.Time
		var v float64
		if rows.Scan(&t, &v) == nil {
			samples = append(samples, anomaly.Sample{T: t.Unix(), Value: v})
		}
	}
	// oldest first for the detector
	for i, j := 0, len(samples)-1; i < j; i, j = i+1, j-1 {
		samples[i], samples[j] = samples[j], samples[i]
	}
	findings, ok := anomaly.Detect(samples, threshold)
	if findings == nil {
		findings = []anomaly.Finding{}
	}
	writeJSON(w, 200, map[string]any{"samples": len(samples), "enough_data": ok, "min_samples": anomaly.MinSamples,
		"threshold": threshold, "hours": hours, "anomalies": findings})
}
