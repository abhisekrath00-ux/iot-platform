-- Recorded position samples, not inferred or averaged locations.
CREATE TABLE IF NOT EXISTS device_positions (
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  lat DOUBLE PRECISION NOT NULL CHECK (lat BETWEEN -90 AND 90),
  lon DOUBLE PRECISION NOT NULL CHECK (lon BETWEEN -180 AND 180),
  recorded_at TIMESTAMPTZ NOT NULL,
  source TEXT NOT NULL CHECK (source IN ('manual','telemetry')),
  PRIMARY KEY (device_id, recorded_at)
);
CREATE INDEX IF NOT EXISTS device_positions_tenant ON device_positions(tenant_id,device_id,recorded_at);
