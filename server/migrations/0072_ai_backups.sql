-- Ordered backup AI providers per tenant. When the active provider fails (unreachable, bad key,
-- quota or rate limit, 5xx) the assistant tries these in order. profile_id is an ai_profiles id or
-- the virtual built-in 'local'. Idempotent.
CREATE TABLE IF NOT EXISTS ai_backups (
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  position INT NOT NULL,
  profile_id TEXT NOT NULL,
  PRIMARY KEY (tenant_id, position),
  UNIQUE (tenant_id, profile_id)
);
