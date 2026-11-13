# Observability (built-in, no Prometheus or Grafana)

Status: backend built and tested (Go tests against Postgres). Web pages: System health and Dev tools (admin only).

## What is collected (real)
- API: requests/min, 4xx and 5xx per min, p50 and p95 latency (from the API process itself).
- Process: goroutines, heap memory.
- Database: size, open connections, ping time.
- Ingest: telemetry rows inserted per minute (from Postgres table statistics).
- Alerts: open and acknowledged counts.
- Logs: warn and error lines written by the API process, plus every 5xx response. Secrets are redacted before storage.

## What is NOT collected
Logs of other services, per-container CPU and RAM, MQTT messages per second. The health response lists these under `sources` so nothing is implied.

## Retention
Metrics sampled every 60 s, kept 14 days. Logs kept 30 days, max 50000 rows. Migration 0064.

## Endpoints (admin only)
- `GET /v1/system/health` current numbers and sources.
- `GET /v1/system/metrics?names=a,b&minutes=60` history.
- `GET /v1/system/logs?level=&q=&minutes=&service=&limit=` stored logs.
- `GET /v1/system/prom` Prometheus text format, for an optional external scraper. Nothing is required.
- `GET /v1/system/support` redacted support bundle (text), audited.

## Failed diagnostics reads

Log lists and support bundles are admin-only and marked `Cache-Control: no-store`. A failed log query, row decode or iteration returns500 rather than partial or empty success. Support bundle log query/decode/iteration failure returns500 before the attachment headers or bundle contents are written; operators can retry after checking availability. This does not certify every support-bundle metric: existing snapshot probes can still omit unavailable values. Redaction remains best-effort and exports need review before sharing. Tests inject scan/iteration errors and close a real Postgres pool to verify no success-shaped JSON or downloadable partial bundle; no production outage was induced.

## API cache and DB pool signals

The admin health snapshot, support bundle and Prometheus endpoint expose the API
replica's response-cache counters and pgx client-pool stats. The minute sampler
also stores them in the existing metric history. No external service is added.

- `cache_enabled` is1 for an enabled response cache,0 when off. Disabled caches
  omit the other cache counters rather than inventing a hit count.
- `cache_hits_total`, `cache_misses_total`, `cache_invalidations_total` count the
  existing response-cache events. An invalidation count means attempted local
  invalidation, not proof every replica has dropped its response.
- `cache_shared_read_failures_total` counts failed or invalid-generation shared
  reads that bypass the cache. `cache_shared_write_failures_total` counts failed
  cache fills; the backend response still succeeds.
- `cache_shared_invalidation_failures_total` counts failed shared generation
  increments. Other replicas may serve old data until entry TTL expires. A rising
  counter needs investigation; no auto-remediation or alert rule is configured.
- `db_pool_acquired`, `db_pool_idle`, `db_pool_total`, `db_pool_max` describe this
  process's client pool, distinct from PostgreSQL-wide `db_connections`.
  `db_pool_acquires_total`, `db_pool_empty_acquires_total`,
  `db_pool_canceled_acquires_total`, `db_pool_acquire_seconds_total` expose pgx's
  acquisition and cumulative wait/duration counters. Empty acquisition means no
  idle connection was available, not necessarily an error or measured outage.

Totals reset with the process; calculate rates with reset-aware tools rather than
subtracting samples across restarts. Prometheus names ending in `_total` use
counter type; other snapshot signals use gauge type. Counters are aggregate:
no tenant, cache key, URI, password or other secret labels are exported. Built-in
charts do not yet graph these signals; admins can request history by the listed
names or use the optional external scraper. One replica's zero failures do not
prove the health of another replica or the shared service. This does not add
cluster aggregation, distributed tracing, SLO alerts or a capacity guarantee.
