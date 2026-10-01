-- Per-tenant opt-in features. Anything that executes tenant-supplied code is
-- off until an admin turns it on. Idempotent (migrations rerun on every start).
CREATE TABLE IF NOT EXISTS tenant_features (
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  feature TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT false,
  updated_by TEXT,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, feature)
);
