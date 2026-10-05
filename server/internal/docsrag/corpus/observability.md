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
