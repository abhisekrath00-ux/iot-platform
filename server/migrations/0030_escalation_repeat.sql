-- Repeat-until-acknowledged: after the last escalation step, resend it every N minutes
-- (capped) while the alert stays open. Idempotent: migrations re-run on every API start.
CREATE TABLE IF NOT EXISTS escalation_settings (
  tenant_id TEXT PRIMARY KEY REFERENCES tenants(id),
  repeat_every_minutes INT NOT NULL DEFAULT 0 CHECK (repeat_every_minutes = 0 OR repeat_every_minutes BETWEEN 5 AND 1440),
  repeat_max INT NOT NULL DEFAULT 0 CHECK (repeat_max BETWEEN 0 AND 10)
);
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS escalation_repeats INT NOT NULL DEFAULT 0;
