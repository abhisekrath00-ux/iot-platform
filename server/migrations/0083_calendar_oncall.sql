-- Optional Google Calendar on-call sync: a schedule may be linked to one
-- calendar; a background sync then derives the current on-call channel from
-- the event spanning now (its summary must contain the channel's target).
-- All columns default to empty/NULL so existing schedules are untouched and
-- the static rotation stays the fallback whenever the calendar has no
-- current event, no matching channel, or a stale/failed sync. Idempotent.
ALTER TABLE oncall_schedules ADD COLUMN IF NOT EXISTS google_calendar_id TEXT NOT NULL DEFAULT '';
ALTER TABLE oncall_schedules ADD COLUMN IF NOT EXISTS cal_channel_id TEXT;
ALTER TABLE oncall_schedules ADD COLUMN IF NOT EXISTS cal_until TIMESTAMPTZ;
ALTER TABLE oncall_schedules ADD COLUMN IF NOT EXISTS cal_summary TEXT NOT NULL DEFAULT '';
ALTER TABLE oncall_schedules ADD COLUMN IF NOT EXISTS cal_synced_at TIMESTAMPTZ;
ALTER TABLE oncall_schedules ADD COLUMN IF NOT EXISTS cal_sync_error TEXT NOT NULL DEFAULT '';
