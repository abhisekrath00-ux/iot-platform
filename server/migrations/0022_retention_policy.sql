-- Daily rollups (built from hourly) outlive hourly rows; per-tenant retention
-- overrides the global RAW_RETENTION_DAYS. Idempotent: migrations re-run on start.
CREATE TABLE IF NOT EXISTS telemetry_rollup_daily (
  tenant_id TEXT NOT NULL,
  device_id TEXT NOT NULL,
  point_id  TEXT NOT NULL,
  bucket    TIMESTAMPTZ NOT NULL,
  n         BIGINT NOT NULL,
  sum       DOUBLE PRECISION NOT NULL,
  min       DOUBLE PRECISION NOT NULL,
  max       DOUBLE PRECISION NOT NULL,
  PRIMARY KEY (tenant_id, device_id, point_id, bucket)
);
CREATE TABLE IF NOT EXISTS tenant_retention (
  tenant_id   TEXT PRIMARY KEY REFERENCES tenants(id),
  raw_days    INT CHECK (raw_days IS NULL OR raw_days BETWEEN 1 AND 3650),
  hourly_days INT CHECK (hourly_days IS NULL OR hourly_days BETWEEN 30 AND 7300),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_by  TEXT
);
