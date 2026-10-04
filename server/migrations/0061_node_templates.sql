-- Admin-defined custom node types: a named, reusable function-node preset. Adding one to a flow copies its
-- code into an ordinary function node, so it runs under the same sandbox, feature gate and admin-only rules.
CREATE TABLE IF NOT EXISTS node_templates (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  code TEXT NOT NULL,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS node_templates_name ON node_templates (tenant_id, lower(name));
