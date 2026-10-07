-- Explicit per-user memory, never inferred from chat. No model writes or cross-user reads.
CREATE TABLE IF NOT EXISTS assistant_memory_settings (
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  user_id TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT false,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, user_id)
);
CREATE TABLE IF NOT EXISTS assistant_memory_notes (
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  user_id TEXT NOT NULL,
  id TEXT NOT NULL,
  role_at_save TEXT NOT NULL,
  title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 120),
  content TEXT NOT NULL CHECK (length(content) BETWEEN 1 AND 2000),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, user_id, id)
);
CREATE INDEX IF NOT EXISTS assistant_memory_owner_expiry ON assistant_memory_notes(tenant_id,user_id,expires_at);
