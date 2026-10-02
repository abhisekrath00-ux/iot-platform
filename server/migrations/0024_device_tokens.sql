-- Per-device ingest tokens for devices that speak plain HTTPS (ESP32, modems,
-- third-party bridges). A token can only write readings for its one device.
-- Only sha256(secret) is stored. Idempotent: migrations re-run on start.
CREATE TABLE IF NOT EXISTS device_tokens (
  id          TEXT PRIMARY KEY,
  tenant_id   TEXT NOT NULL REFERENCES tenants(id),
  device_id   TEXT NOT NULL,
  name        TEXT NOT NULL,
  secret_hash BYTEA NOT NULL,
  created_by  TEXT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at  TIMESTAMPTZ NOT NULL,
  revoked_at  TIMESTAMPTZ,
  last_used_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS device_tokens_device ON device_tokens(tenant_id, device_id);
