-- Hourly rollups survive raw-data retention. avg = sum / count.
CREATE TABLE IF NOT EXISTS telemetry_rollup_hourly (
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
