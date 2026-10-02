-- Per-gateway alarm outputs and edge-local rules. They are rendered into the
-- gateway's edge-config YAML and run on the edge box with or without the server.
CREATE TABLE IF NOT EXISTS gateway_edge_rules (
  gateway_id TEXT PRIMARY KEY REFERENCES gateways(id),
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  outputs JSONB NOT NULL DEFAULT '[]',
  rules JSONB NOT NULL DEFAULT '[]',
  updated_by TEXT,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
