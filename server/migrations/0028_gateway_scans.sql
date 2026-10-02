-- Dashboard-requested discovery scans. The gateway answers on t/<tenant>/g/<gw>/scan/result
-- and the ingest service stores the result here. Scans are read-only on the edge.
CREATE TABLE IF NOT EXISTS gateway_scans (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  gateway_id TEXT NOT NULL REFERENCES gateways(id),
  kind TEXT NOT NULL,
  params JSONB NOT NULL DEFAULT '{}',
  status TEXT NOT NULL DEFAULT 'requested',
  result JSONB,
  requested_by TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS gateway_scans_gw ON gateway_scans(tenant_id, gateway_id, created_at DESC);
