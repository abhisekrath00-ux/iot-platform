// tsbench measures the Postgres telemetry store with a synthetic load so that
// any store decision rests on numbers from the target hardware, not opinion.
//
//	BENCH_DATABASE_URL=postgres://... go run ./cmd/tsbench -devices 200 -points 5 -hours 48
//
// It writes into a dedicated tenant ("tsbench") and deletes it afterwards.
// Output is one JSON object; commit it with the hardware description.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const tenant = "tsbench"

func pct(d []time.Duration, p float64) float64 {
	if len(d) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return float64(s[int(math.Min(float64(len(s)-1), math.Ceil(p*float64(len(s)))-1))].Microseconds()) / 1000
}

func main() {
	devices := flag.Int("devices", 100, "devices")
	points := flag.Int("points", 5, "points per device")
	hours := flag.Int("hours", 24, "hours of history to generate")
	every := flag.Int("interval", 60, "seconds between samples")
	batch := flag.Int("batch", 5000, "rows per COPY batch")
	keep := flag.Bool("keep", false, "do not delete the generated data")
	flag.Parse()
	url := os.Getenv("BENCH_DATABASE_URL")
	if url == "" {
		fmt.Fprintln(os.Stderr, "set BENCH_DATABASE_URL")
		os.Exit(2)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		panic(err)
	}
	defer pool.Close()
	pool.Exec(ctx, `INSERT INTO tenants(id,name) VALUES($1,$1) ON CONFLICT DO NOTHING`, tenant)
	cleanup := func() {
		pool.Exec(ctx, `DELETE FROM telemetry WHERE tenant_id=$1`, tenant)
		pool.Exec(ctx, `DELETE FROM telemetry_rollup_hourly WHERE tenant_id=$1`, tenant)
	}
	cleanup()
	if !*keep {
		defer cleanup()
	}
	end := time.Now().UTC().Truncate(time.Minute)
	start := end.Add(-time.Duration(*hours) * time.Hour)
	rng := rand.New(rand.NewSource(1))
	type row []any
	var rows [][]any
	var total int64
	var batchLat []time.Duration
	ingestStart := time.Now()
	flush := func() {
		if len(rows) == 0 {
			return
		}
		t0 := time.Now()
		_, err := pool.CopyFrom(ctx, pgx.Identifier{"telemetry"},
			[]string{"event_id", "tenant_id", "gateway_id", "device_id", "point_id", "observed_at", "value", "unit", "quality", "schema_version"},
			pgx.CopyFromRows(rows))
		if err != nil {
			panic(err)
		}
		batchLat = append(batchLat, time.Since(t0))
		total += int64(len(rows))
		rows = rows[:0]
	}
	n := 0
	for ts := start; ts.Before(end); ts = ts.Add(time.Duration(*every) * time.Second) {
		for d := 0; d < *devices; d++ {
			for p := 0; p < *points; p++ {
				n++
				v := 20 + 5*math.Sin(float64(ts.Unix())/3600) + rng.NormFloat64()
				rows = append(rows, row{fmt.Sprintf("tsb-%d", n), tenant, "g1", fmt.Sprintf("dev-%04d", d), fmt.Sprintf("p%d", p), ts, v, "C", "measured", 1})
				if len(rows) >= *batch {
					flush()
				}
			}
		}
	}
	flush()
	ingestSecs := time.Since(ingestStart).Seconds()

	timeQ := func(runs int, q string, args ...any) []time.Duration {
		var out []time.Duration
		for i := 0; i < runs; i++ {
			t0 := time.Now()
			r, err := pool.Query(ctx, q, args...)
			if err != nil {
				panic(err)
			}
			for r.Next() {
			}
			r.Close()
			out = append(out, time.Since(t0))
		}
		return out
	}
	t0 := time.Now()
	pool.Exec(ctx, `INSERT INTO telemetry_rollup_hourly(tenant_id, device_id, point_id, bucket, n, sum, min, max)
	  SELECT tenant_id, device_id, point_id, date_trunc('hour', observed_at), count(*), sum(value), min(value), max(value)
	  FROM telemetry WHERE tenant_id=$1 GROUP BY 1,2,3,4 ON CONFLICT DO NOTHING`, tenant)
	rollupSecs := time.Since(t0).Seconds()
	q24 := timeQ(30, `SELECT observed_at, value FROM telemetry WHERE tenant_id=$1 AND device_id='dev-0000' AND point_id='p0' AND observed_at > now() - interval '24 hours' ORDER BY observed_at`, tenant)
	qHourly := timeQ(30, `SELECT bucket, sum/n, min, max FROM telemetry_rollup_hourly WHERE tenant_id=$1 AND device_id='dev-0000' AND point_id='p0' ORDER BY bucket`, tenant)
	qDash := timeQ(10, `SELECT device_id, point_id, max(observed_at), avg(value) FROM telemetry WHERE tenant_id=$1 AND device_id < 'dev-0050' AND observed_at > now() - interval '1 hour' GROUP BY 1,2`, tenant)
	var size int64
	pool.QueryRow(ctx, `SELECT COALESCE(sum(pg_total_relation_size(c.oid)),0) FROM pg_class c JOIN pg_inherits i ON i.inhrelid=c.oid JOIN pg_class p ON p.oid=i.inhparent WHERE p.relname='telemetry'`).Scan(&size)
	var version string
	pool.QueryRow(ctx, `SHOW server_version`).Scan(&version)
	res := map[string]any{
		"engine": "postgres " + version, "devices": *devices, "points_per_device": *points, "hours": *hours, "interval_s": *every,
		"rows": total, "ingest_rows_per_s": math.Round(float64(total) / ingestSecs),
		"copy_batch_ms_p50": pct(batchLat, .5), "copy_batch_ms_p99": pct(batchLat, .99),
		"rollup_build_s":       math.Round(rollupSecs*100) / 100,
		"query_24h_raw_ms_p50": pct(q24, .5), "query_24h_raw_ms_p95": pct(q24, .95),
		"query_hourly_ms_p50": pct(qHourly, .5), "query_hourly_ms_p95": pct(qHourly, .95),
		"query_50dev_dashboard_ms_p50": pct(qDash, .5), "query_50dev_dashboard_ms_p95": pct(qDash, .95),
		"table_bytes_all_partitions": size,
		"note":                       "run on this machine; not a production figure. Table size counts every tenant in the database.",
	}
	json.NewEncoder(os.Stdout).Encode(res)
}
