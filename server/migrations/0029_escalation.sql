-- Alert escalation: when an alert stays open and unacknowledged, notify more channels
-- after set delays. Steps are per tenant, optionally per severity ('' = any).
-- Idempotent: migrations re-run on every API start.
CREATE TABLE IF NOT EXISTS escalation_steps (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  severity TEXT NOT NULL DEFAULT '',
  step INT NOT NULL CHECK (step BETWEEN 1 AND 5),
  after_minutes INT NOT NULL CHECK (after_minutes BETWEEN 1 AND 10080),
  channel_id TEXT NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
  UNIQUE (tenant_id, severity, step)
);
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS escalation_level INT NOT NULL DEFAULT 0;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS escalated_at TIMESTAMPTZ;
