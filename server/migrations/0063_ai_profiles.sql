-- Saved AI provider profiles. ai_settings stays the single ACTIVE connection; activating a profile copies it there.
-- API keys live in the encrypted secrets store under key_secret ("ai-key-<id>"), never here. Idempotent.
CREATE TABLE IF NOT EXISTS ai_profiles (
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  id TEXT NOT NULL,
  name TEXT NOT NULL,
  base_url TEXT NOT NULL,
  model TEXT NOT NULL,
  key_secret TEXT NOT NULL DEFAULT '',
  updated_by TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, id)
);
CREATE UNIQUE INDEX IF NOT EXISTS ai_profiles_name ON ai_profiles (tenant_id, lower(name));
