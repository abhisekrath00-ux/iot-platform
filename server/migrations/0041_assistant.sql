-- AI assistant: the tenant's model connection and the changes the assistant has proposed.
-- The API key is never stored here; it lives in the encrypted secrets store under key_secret.
-- Idempotent: migrations re-run on every API start.
CREATE TABLE IF NOT EXISTS ai_settings (
  tenant_id TEXT PRIMARY KEY REFERENCES tenants(id),
  enabled BOOLEAN NOT NULL DEFAULT false,
  base_url TEXT NOT NULL DEFAULT '',        -- OpenAI-compatible, e.g. http://ollama:11434/v1
  model TEXT NOT NULL DEFAULT '',
  key_secret TEXT NOT NULL DEFAULT '',      -- name in the secrets store, '' = no key (local servers)
  updated_by TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- A change the assistant wants to make. It runs only when the same user confirms it in the UI.
CREATE TABLE IF NOT EXISTS assistant_actions (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  user_id TEXT NOT NULL,
  method TEXT NOT NULL,
  path TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','executed','failed','rejected','expired')),
  result_code INT,
  result_body TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  decided_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS assistant_actions_user ON assistant_actions (tenant_id, user_id, created_at DESC);
