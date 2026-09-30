-- Alarm lifecycle: who acknowledged / resolved an alert and when, plus a
-- comment trail. Status values stay open | acknowledged | resolved.
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS acknowledged_by TEXT;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS acknowledged_at TIMESTAMPTZ;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS resolved_by TEXT;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS resolved_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS alert_comments (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  alert_id TEXT NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
  author TEXT NOT NULL,
  body TEXT NOT NULL CHECK (length(body) BETWEEN 1 AND 2000),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS alert_comments_alert_idx ON alert_comments(alert_id, created_at);
