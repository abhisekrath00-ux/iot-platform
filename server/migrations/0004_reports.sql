-- Report builder: saved report definitions and run history.
CREATE TABLE IF NOT EXISTS reports (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  name TEXT NOT NULL,
  definition JSONB NOT NULL,   -- {metrics:[{device_id,point_id}], window_hours, group_by}
  schedule_cron TEXT,          -- NULL = on demand only; "0 6 * * *" daily 6am etc.
  channel_id TEXT,             -- notification_channels row to deliver to
  last_run_at TIMESTAMPTZ,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS report_runs (
  id TEXT PRIMARY KEY,
  report_id TEXT NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  finished_at TIMESTAMPTZ,
  status TEXT NOT NULL DEFAULT 'running', -- running|ok|failed
  error TEXT,
  row_count INT
);
CREATE INDEX IF NOT EXISTS report_runs_report ON report_runs(report_id, started_at DESC);
