-- Maintenance windows: while one covers a device, new non-critical alerts for it are recorded but not notified.
-- Idempotent (migrations re-run on every API start).
CREATE TABLE IF NOT EXISTS maintenance_windows (
  id         TEXT PRIMARY KEY,
  tenant_id  TEXT NOT NULL REFERENCES tenants(id),
  name       TEXT NOT NULL,
  device_id  TEXT,
  asset_id   TEXT REFERENCES assets(id),
  starts_at  TIMESTAMPTZ NOT NULL,
  ends_at    TIMESTAMPTZ NOT NULL,
  ended_at   TIMESTAMPTZ,
  created_by TEXT NOT NULL REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK ((device_id IS NULL) <> (asset_id IS NULL)),
  CHECK (ends_at > starts_at AND ends_at <= starts_at + interval '7 days')
);
CREATE INDEX IF NOT EXISTS maintenance_windows_tenant ON maintenance_windows(tenant_id, ends_at);
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS shelved BOOLEAN NOT NULL DEFAULT false;
