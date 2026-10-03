-- Quiet hours for escalation reminders (never for critical alerts or first-time steps).
-- Idempotent: migrations re-run on every API start.
ALTER TABLE escalation_settings ADD COLUMN IF NOT EXISTS quiet_start TEXT NOT NULL DEFAULT '';
ALTER TABLE escalation_settings ADD COLUMN IF NOT EXISTS quiet_end TEXT NOT NULL DEFAULT '';
ALTER TABLE escalation_settings ADD COLUMN IF NOT EXISTS quiet_tz TEXT NOT NULL DEFAULT '';
