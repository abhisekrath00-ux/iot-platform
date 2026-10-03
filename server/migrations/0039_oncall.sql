-- On-call rotations: a schedule hands the on-call duty to each notification channel in turn, and an
-- escalation step may name a schedule instead of one fixed channel. Idempotent.
CREATE TABLE IF NOT EXISTS oncall_schedules (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  name TEXT NOT NULL,
  anchor TIMESTAMPTZ NOT NULL,            -- when the first channel's shift began
  shift_hours INT NOT NULL CHECK (shift_hours BETWEEN 1 AND 720),
  channel_ids TEXT[] NOT NULL CHECK (cardinality(channel_ids) BETWEEN 1 AND 20),
  created_by TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, name)
);
ALTER TABLE escalation_steps ALTER COLUMN channel_id DROP NOT NULL;
ALTER TABLE escalation_steps ADD COLUMN IF NOT EXISTS schedule_id TEXT REFERENCES oncall_schedules(id) ON DELETE RESTRICT;
DO $$ BEGIN
  ALTER TABLE escalation_steps ADD CONSTRAINT escalation_steps_target CHECK ((channel_id IS NULL) <> (schedule_id IS NULL));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
