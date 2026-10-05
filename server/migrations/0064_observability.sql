-- Built-in observability: one-minute metric samples and a bounded store of warning/error log lines.
-- Retention is enforced by the API (14 days of metrics, 30 days and 50000 rows of logs). Idempotent.
CREATE TABLE IF NOT EXISTS obs_metrics (
  ts TIMESTAMPTZ NOT NULL,
  instance TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL,
  value DOUBLE PRECISION NOT NULL
);
CREATE INDEX IF NOT EXISTS obs_metrics_name_ts ON obs_metrics (name, ts DESC);
CREATE TABLE IF NOT EXISTS obs_logs (
  id BIGSERIAL PRIMARY KEY,
  ts TIMESTAMPTZ NOT NULL DEFAULT now(),
  service TEXT NOT NULL DEFAULT 'api',
  level TEXT NOT NULL CHECK (level IN ('warn','error')),
  message TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS obs_logs_ts ON obs_logs (ts DESC);
CREATE INDEX IF NOT EXISTS obs_logs_level_ts ON obs_logs (level, ts DESC);
