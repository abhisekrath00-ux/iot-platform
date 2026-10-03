-- Report versions: every edit or restore snapshots the previous state, so history is never lost.
-- Idempotent: migrations re-run on every API start.
ALTER TABLE reports ADD COLUMN IF NOT EXISTS version INT NOT NULL DEFAULT 1;
CREATE TABLE IF NOT EXISTS report_versions (
  report_id TEXT NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
  version INT NOT NULL,
  name TEXT NOT NULL,
  definition JSONB NOT NULL,
  schedule_cron TEXT,
  channel_id TEXT,
  superseded_by TEXT NOT NULL,
  superseded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (report_id, version)
);
