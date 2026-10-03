-- Commands raised by a flow control node carry where they came from, so approvers can see why. Idempotent.
ALTER TABLE commands ADD COLUMN IF NOT EXISTS source_flow_id TEXT;
ALTER TABLE commands ADD COLUMN IF NOT EXISTS control_target_id TEXT;
ALTER TABLE commands ADD COLUMN IF NOT EXISTS reason TEXT;
CREATE INDEX IF NOT EXISTS commands_target_time ON commands(control_target_id, created_at) WHERE control_target_id IS NOT NULL;
