-- Custom roles: a built-in base role minus denied capability groups. A custom role can only restrict.
CREATE TABLE IF NOT EXISTS custom_roles (
  id         TEXT PRIMARY KEY,
  tenant_id  TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  base_role  TEXT NOT NULL CHECK (base_role IN ('operator','installer','viewer')),
  denied     TEXT[] NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, name)
);
ALTER TABLE users ADD COLUMN IF NOT EXISTS custom_role_id TEXT REFERENCES custom_roles(id);
