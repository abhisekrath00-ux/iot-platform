-- Flow builder: trigger/condition/delay/notify pipelines, tenant-defined.
CREATE TABLE IF NOT EXISTS flows (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  name TEXT NOT NULL,
  definition JSONB NOT NULL,   -- {trigger:{type,device_id,point_id,op,value}, steps:[...]}
  enabled BOOLEAN NOT NULL DEFAULT true,
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS flow_runs (
  id TEXT PRIMARY KEY,
  flow_id TEXT NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
  trigger_value DOUBLE PRECISION,
  outcome TEXT NOT NULL,       -- notified|skipped_condition|failed
  detail TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS flows_tenant_enabled ON flows(tenant_id) WHERE enabled;
