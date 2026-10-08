-- Durable record of every assistant run, so a dropped connection or a restart leaves an honest status and a
-- replayable answer instead of nothing. A run that was in flight when the server stopped is marked interrupted.
CREATE TABLE IF NOT EXISTS assistant_runs (
  id          TEXT NOT NULL,
  tenant_id   TEXT NOT NULL,
  user_id     TEXT NOT NULL,
  status      TEXT NOT NULL CHECK (status IN ('running','done','failed','interrupted')),
  result      JSONB,
  error       TEXT NOT NULL DEFAULT '',
  started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  finished_at TIMESTAMPTZ,
  PRIMARY KEY (tenant_id, id)
);
CREATE INDEX IF NOT EXISTS assistant_runs_user ON assistant_runs (tenant_id, user_id, started_at DESC);
