-- Tracked sign-in sessions: one row per login (local, SSO or invite accept),
-- id embedded in the token as jti so one session can be listed and revoked.
CREATE TABLE IF NOT EXISTS user_sessions (
  id          TEXT PRIMARY KEY,
  tenant_id   TEXT NOT NULL REFERENCES tenants(id),
  user_id     TEXT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ip          TEXT,
  user_agent  TEXT,
  revoked_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS user_sessions_user ON user_sessions (tenant_id, user_id) WHERE revoked_at IS NULL;
