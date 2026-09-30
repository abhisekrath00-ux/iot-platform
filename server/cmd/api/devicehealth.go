package main

import (
	"net/http"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
	"github.com/abhisekrath00-ux/iot-platform/server/internal/health"
)

// GET /v1/devices/{id}/health: score, status and the factors behind it.
func (s *server) deviceHealth(w http.ResponseWriter, r *http.Request) {
	id, tenant := r.PathValue("id"), auth.Tenant(r)
	var gwStatus string
	var gwSeen *time.Time
	var interval *float64
	err := s.st.Pool.QueryRow(r.Context(),
		`SELECT g.status, g.last_seen_at, (d.config->'connection'->>'interval_seconds')::float8
		 FROM devices d JOIN gateways g ON g.id=d.gateway_id WHERE d.id=$1 AND d.tenant_id=$2`, id, tenant).Scan(&gwStatus, &gwSeen, &interval)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	var last *time.Time
	var n, measured int
	s.st.Pool.QueryRow(r.Context(),
		`SELECT max(observed_at), count(*), count(*) FILTER (WHERE quality='measured') FROM (
		   SELECT observed_at, quality FROM telemetry WHERE tenant_id=$1 AND device_id=$2 ORDER BY observed_at DESC LIMIT 50) x`,
		tenant, id).Scan(&last, &n, &measured)
	now := time.Now()
	recent := func(t *time.Time) bool { return t != nil && now.Sub(*t) < 10*time.Minute }
	in := health.Input{LastSeen: last, Now: now, Samples: n, MeasuredSamples: measured,
		GatewayOnline: gwStatus == "active" && (recent(gwSeen) || recent(last))}
	if interval != nil && *interval > 0 {
		in.Interval = time.Duration(*interval * float64(time.Second))
	}
	writeJSON(w, 200, health.Score(in))
}
