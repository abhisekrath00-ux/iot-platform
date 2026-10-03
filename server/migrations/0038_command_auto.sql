-- Commands raised under an admin's "automatic" setting on an alarm-output control target.
-- The flag is set only by the flow engine's control node, never by an API caller.
ALTER TABLE commands ADD COLUMN IF NOT EXISTS auto_approved BOOLEAN NOT NULL DEFAULT false;
CREATE INDEX IF NOT EXISTS commands_auto_pending ON commands (expires_at) WHERE auto_approved AND status = 'approved';
