-- Numeric state kept between flow runs. scope is 'global' (whole tenant) or 'flow/<flow id>'.
CREATE TABLE IF NOT EXISTS flow_context (
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  scope TEXT NOT NULL,
  key TEXT NOT NULL,
  value DOUBLE PRECISION NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, scope, key)
);
