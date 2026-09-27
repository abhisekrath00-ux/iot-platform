-- Rules get a concrete v1 shape; notification channels per tenant.
CREATE TABLE IF NOT EXISTS notification_channels (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  type TEXT NOT NULL CHECK (type IN ('email','slack')),
  target TEXT NOT NULL,          -- email address or slack channel id
  enabled BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS rules_tenant_enabled ON rules (tenant_id) WHERE enabled;
