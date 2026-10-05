package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/abhisekrath00-ux/iot-platform/server/internal/auth"
)

// Built-in observability: request metrics, a one-minute sampler into Postgres and a bounded store of
// warning/error log lines from this process. Admin-only views. No Prometheus or Grafana needed.
// Real sources only: this API process, the database, and the telemetry insert counter. Per-container
// CPU/RAM and MQTT message rates are NOT collected here (see docs/observability.md).

const (
	obsMetricDays = 14
	obsLogDays    = 30
	obsLogCap     = 50000
)

var obs = struct {
	reqs, e5xx, e4xx atomic.Int64
	started          time.Time
	mu               sync.Mutex
	lat              []float64 // latencies in ms since the last sample
	lastSample       atomic.Int64
	logCh            chan obsLog
	prevIns          float64
	prevReq, prevErr int64
}{started: time.Now(), logCh: make(chan obsLog, 512)}

type obsLog struct {
	level, service, msg string
	at                  time.Time
}

var (
	reSecretKV  = regexp.MustCompile(`(?i)((password|passwd|secret|token|api[_-]?key|authorization)["'\s:=]+(bearer\s+)?)[^\s"',;]{6,}`)
	reDBURL     = regexp.MustCompile(`(?i)(postgres(ql)?://[^:/\s]+:)[^@\s]+@`)
	reSKKey     = regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{12,}`)
	reLevelErr  = regexp.MustCompile(`(?i)\b(error|fatal|panic|failed|failure)\b`)
	reLevelWarn = regexp.MustCompile(`(?i)\b(warn|warning|retry|timeout|refused)\b`)
)

func obsRedact(s string) string {
	s = reDBURL.ReplaceAllString(s, "${1}<redacted>@")
	s = reSKKey.ReplaceAllString(s, "sk-<redacted>")
	s = reSecretKV.ReplaceAllString(s, "${1}<redacted>")
	if len(s) > 2000 {
		s = s[:2000] + "..."
	}
	return s
}

// obsTee receives every line the standard logger writes and keeps the warning and error ones.
type obsTee struct{}

func (obsTee) Write(p []byte) (int, error) {
	line := strings.TrimSpace(string(p))
	if line == "" {
		return len(p), nil
	}
	lvl := ""
	switch {
	case reLevelErr.MatchString(line):
		lvl = "error"
	case reLevelWarn.MatchString(line):
		lvl = "warn"
	}
	if lvl != "" {
		select {
		case obs.logCh <- obsLog{lvl, "api", obsRedact(line), time.Now()}:
		default: // never block logging
		}
	}
	return len(p), nil
}

func installObsLogging() { log.SetOutput(io.MultiWriter(os.Stderr, obsTee{})) }

type statusRec struct {
	http.ResponseWriter
	code int
}

func (s *statusRec) WriteHeader(c int) { s.code = c; s.ResponseWriter.WriteHeader(c) }
func (s *statusRec) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// obsMiddleware counts requests, errors and latency. It records 5xx responses as error log lines.
func obsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t0 := time.Now()
		rec := &statusRec{ResponseWriter: w, code: 200}
		next.ServeHTTP(rec, r)
		ms := float64(time.Since(t0).Microseconds()) / 1000
		obs.reqs.Add(1)
		switch {
		case rec.code >= 500:
			obs.e5xx.Add(1)
			select {
			case obs.logCh <- obsLog{"error", "api", obsRedact(fmt.Sprintf("%s %s -> %d in %.0f ms", r.Method, r.URL.Path, rec.code, ms)), time.Now()}:
			default:
			}
		case rec.code >= 400:
			obs.e4xx.Add(1)
		}
		obs.mu.Lock()
		if len(obs.lat) < 5000 {
			obs.lat = append(obs.lat, ms)
		}
		obs.mu.Unlock()
	})
}

func pct(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(float64(len(sorted)-1) * p)
	return sorted[i]
}

// obsSample reads the current numbers. Used by the sampler and by the live health view.
func (s *server) obsSnapshot(ctx context.Context) map[string]float64 {
	m := map[string]float64{}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	m["api_goroutines"] = float64(runtime.NumGoroutine())
	m["api_heap_mb"] = float64(ms.HeapAlloc) / (1 << 20)
	m["api_requests_total"] = float64(obs.reqs.Load())
	m["api_errors_5xx_total"] = float64(obs.e5xx.Load())
	m["api_errors_4xx_total"] = float64(obs.e4xx.Load())
	var db, conns, ins, open, ack float64
	t0 := time.Now()
	if err := s.st.Pool.QueryRow(ctx, `SELECT pg_database_size(current_database())`).Scan(&db); err == nil {
		m["db_ping_ms"] = float64(time.Since(t0).Microseconds()) / 1000
		m["db_size_mb"] = db / (1 << 20)
	}
	if s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database()`).Scan(&conns) == nil {
		m["db_connections"] = conns
	}
	if s.st.Pool.QueryRow(ctx, `SELECT COALESCE(sum(n_tup_ins),0) FROM pg_stat_user_tables WHERE relname LIKE 'telemetry%' AND relname NOT LIKE '%rollup%'`).Scan(&ins) == nil {
		m["telemetry_rows_inserted_total"] = ins
	}
	if s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE status='open'`).Scan(&open) == nil {
		m["alerts_open"] = open
	}
	if s.st.Pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE status='acknowledged'`).Scan(&ack) == nil {
		m["alerts_acknowledged"] = ack
	}
	return m
}

// obsRun samples every minute and drains the log channel. Runs until ctx ends.
func (s *server) obsRun(ctx context.Context) {
	host, _ := os.Hostname()
	tick := time.NewTicker(60 * time.Second)
	defer tick.Stop()
	flush := time.NewTicker(2 * time.Second)
	defer flush.Stop()
	sample := func() {
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		snap := s.obsSnapshot(cctx)
		obs.mu.Lock()
		lat := obs.lat
		obs.lat = nil
		obs.mu.Unlock()
		sort.Float64s(lat)
		req, e5 := obs.reqs.Load(), obs.e5xx.Load()
		out := map[string]float64{
			"api_requests_per_min": float64(req - obs.prevReq), "api_errors_per_min": float64(e5 - obs.prevErr),
			"api_p50_ms": pct(lat, 0.5), "api_p95_ms": pct(lat, 0.95),
		}
		obs.prevReq, obs.prevErr = req, e5
		if ins, ok := snap["telemetry_rows_inserted_total"]; ok {
			if obs.prevIns > 0 && ins >= obs.prevIns {
				out["ingest_rows_per_min"] = ins - obs.prevIns
			}
			obs.prevIns = ins
		}
		for _, k := range []string{"api_goroutines", "api_heap_mb", "db_size_mb", "db_connections", "db_ping_ms", "alerts_open", "alerts_acknowledged"} {
			if v, ok := snap[k]; ok {
				out[k] = v
			}
		}
		now := time.Now()
		for k, v := range out {
			s.st.Pool.Exec(cctx, `INSERT INTO obs_metrics(ts,instance,name,value) VALUES($1,$2,$3,$4)`, now, host, k, v)
		}
		s.st.Pool.Exec(cctx, `DELETE FROM obs_metrics WHERE ts < now() - make_interval(days => $1)`, obsMetricDays)
		s.st.Pool.Exec(cctx, `DELETE FROM obs_logs WHERE ts < now() - make_interval(days => $1) OR id < (SELECT COALESCE(max(id),0) - $2 FROM obs_logs)`, obsLogDays, obsLogCap)
		obs.lastSample.Store(now.Unix())
	}
	drain := func() {
		for {
			select {
			case l := <-obs.logCh:
				s.st.Pool.Exec(ctx, `INSERT INTO obs_logs(ts,service,level,message) VALUES($1,$2,$3,$4)`, l.at, l.service, l.level, l.msg)
			default:
				return
			}
		}
	}
	sample()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			sample()
		case <-flush.C:
			drain()
		}
	}
}

// GET /v1/system/health (admin): live numbers, what is collected and what is not.
func (s *server) systemHealth(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	snap := s.obsSnapshot(r.Context())
	var lastErr string
	s.st.Pool.QueryRow(r.Context(), `SELECT COALESCE(to_char(max(ts),'YYYY-MM-DD"T"HH24:MI:SS"Z"'),'') FROM obs_logs WHERE level='error'`).Scan(&lastErr)
	var e24, w24 int
	s.st.Pool.QueryRow(r.Context(), `SELECT count(*) FILTER (WHERE level='error'), count(*) FILTER (WHERE level='warn') FROM obs_logs WHERE ts > now() - interval '24 hours'`).Scan(&e24, &w24)
	writeJSON(w, 200, map[string]any{
		"now": time.Now().UTC(), "uptime_seconds": int(time.Since(obs.started).Seconds()), "go": runtime.Version(),
		"current": snap, "errors_24h": e24, "warnings_24h": w24, "last_error_at": lastErr,
		"last_sample_unix": obs.lastSample.Load(),
		"retention":        map[string]any{"metric_days": obsMetricDays, "log_days": obsLogDays, "log_rows": obsLogCap, "sample_seconds": 60},
		"sources": []map[string]any{
			{"name": "API process (requests, latency, errors, memory)", "available": true},
			{"name": "Database (size, connections, ping, alert counts)", "available": true},
			{"name": "Ingest rate (telemetry rows inserted per minute)", "available": true},
			{"name": "Warning and error log lines from the API", "available": true},
			{"name": "Ingest and other services' own logs", "available": false, "note": "only the API's logs are captured; use hexthings logs <service>"},
			{"name": "Per-container CPU and memory", "available": false, "note": "needs the Docker socket; use hexthings dash"},
			{"name": "MQTT messages per second", "available": false, "note": "the broker's $SYS stats are not read yet"},
		},
	})
}

// GET /v1/system/metrics?names=a,b&minutes=60 (admin): time series from the sampler.
func (s *server) systemMetrics(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	mins, _ := strconv.Atoi(r.URL.Query().Get("minutes"))
	if mins <= 0 || mins > obsMetricDays*1440 {
		mins = 60
	}
	var names []string
	for _, n := range strings.Split(r.URL.Query().Get("names"), ",") {
		if n = strings.TrimSpace(n); n != "" && len(n) < 60 {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		http.Error(w, "names is required", 400)
		return
	}
	rows, err := s.st.Pool.Query(r.Context(), `SELECT name, extract(epoch from ts)::bigint, avg(value) FROM obs_metrics
		WHERE name = ANY($1) AND ts > now() - make_interval(mins => $2) GROUP BY name, ts ORDER BY ts`, names, mins)
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer rows.Close()
	out := map[string][][2]float64{}
	for rows.Next() {
		var n string
		var t int64
		var v float64
		if rows.Scan(&n, &t, &v) == nil {
			out[n] = append(out[n], [2]float64{float64(t), v})
		}
	}
	writeJSON(w, 200, map[string]any{"series": out, "minutes": mins})
}

// GET /v1/system/logs?level=error|warn&q=text&minutes=N&limit=N (admin).
func (s *server) systemLogs(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	q := r.URL.Query()
	lvl := q.Get("level")
	if lvl != "error" && lvl != "warn" {
		lvl = ""
	}
	mins, _ := strconv.Atoi(q.Get("minutes"))
	if mins <= 0 || mins > obsLogDays*1440 {
		mins = 1440
	}
	lim, _ := strconv.Atoi(q.Get("limit"))
	if lim <= 0 || lim > 500 {
		lim = 200
	}
	text := strings.TrimSpace(q.Get("q"))
	if len(text) > 100 {
		text = text[:100]
	}
	rows, err := s.st.Pool.Query(r.Context(), `SELECT id, ts, service, level, message FROM obs_logs
		WHERE ts > now() - make_interval(mins => $1) AND ($2 = '' OR level = $2) AND ($3 = '' OR message ILIKE '%' || $3 || '%') AND ($4 = '' OR service = $4)
		ORDER BY id DESC LIMIT $5`, mins, lvl, text, q.Get("service"), lim)
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var ts time.Time
		var svc, l, msg string
		if rows.Scan(&id, &ts, &svc, &l, &msg) == nil {
			out = append(out, map[string]any{"id": id, "ts": ts, "service": svc, "level": l, "message": msg})
		}
	}
	writeJSON(w, 200, map[string]any{"logs": out})
}

// GET /v1/system/prom (admin): the same numbers in Prometheus text format, for an outside scraper.
func (s *server) systemProm(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	snap := s.obsSnapshot(r.Context())
	keys := make([]string, 0, len(snap))
	for k := range snap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	for _, k := range keys {
		fmt.Fprintf(w, "# TYPE hexthings_%s gauge\nhexthings_%s %g\n", k, k, snap[k])
	}
}

// GET /v1/system/support (admin): a plain-text bundle for support. Secrets are redacted; no env values.
func (s *server) systemSupport(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "HexThings support bundle, %s\nuptime %s, %s\n\n[current numbers]\n", time.Now().UTC().Format(time.RFC3339), time.Since(obs.started).Round(time.Second), runtime.Version())
	snap := s.obsSnapshot(r.Context())
	keys := make([]string, 0, len(snap))
	for k := range snap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "%s = %g\n", k, snap[k])
	}
	b.WriteString("\n[configuration variables that are set (names only)]\n")
	var names []string
	for _, e := range os.Environ() {
		if i := strings.Index(e, "="); i > 0 {
			names = append(names, e[:i])
		}
	}
	sort.Strings(names)
	b.WriteString(strings.Join(names, "\n"))
	b.WriteString("\n\n[last 500 warning and error lines, redacted]\n")
	rows, err := s.st.Pool.Query(r.Context(), `SELECT ts, level, message FROM obs_logs ORDER BY id DESC LIMIT 500`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var ts time.Time
			var l, m string
			if rows.Scan(&ts, &l, &m) == nil {
				fmt.Fprintf(&b, "%s %s %s\n", ts.UTC().Format(time.RFC3339), l, obsRedact(m))
			}
		}
	}
	s.audit(r, "system.support_bundle", "", nil)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="hexthings-support.txt"`)
	w.Write([]byte(b.String()))
}

var _ = auth.Tenant
