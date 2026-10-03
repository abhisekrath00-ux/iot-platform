-- Where a device is, and named circular zones to compare against. Location is metadata set by a person;
-- it is not read from telemetry. Computed on read, no enter/exit events.
ALTER TABLE devices ADD COLUMN IF NOT EXISTS lat DOUBLE PRECISION;
ALTER TABLE devices ADD COLUMN IF NOT EXISTS lon DOUBLE PRECISION;
CREATE TABLE IF NOT EXISTS geofences (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  name TEXT NOT NULL,
  lat DOUBLE PRECISION NOT NULL,
  lon DOUBLE PRECISION NOT NULL,
  radius_m DOUBLE PRECISION NOT NULL,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS geofences_tenant ON geofences (tenant_id);
