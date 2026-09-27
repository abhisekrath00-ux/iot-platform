-- Guided commissioning: one row per installer wizard run. The flow is
-- site -> claim code (QR) -> gateway claim -> device profile -> port test
-- -> live preview. port_test holds {requested_at, config, result}; the
-- authoritative step state is derived at read time from gateway status,
-- device linkage, test result, and first telemetry (see internal/commission).
CREATE TABLE IF NOT EXISTS commissioning_sessions (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  site_id TEXT NOT NULL,
  gateway_id TEXT NOT NULL REFERENCES gateways(id),
  serial TEXT NOT NULL,
  device_id TEXT,
  profile_id TEXT,
  port_test JSONB,
  state TEXT NOT NULL DEFAULT 'awaiting_claim'
    CHECK (state IN ('awaiting_claim','claimed','profiled','tested','live','failed')),
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS commissioning_sessions_tenant
  ON commissioning_sessions(tenant_id, created_at DESC);
