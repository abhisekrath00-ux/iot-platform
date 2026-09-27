-- Schema v1. Idempotent. Telemetry is range-partitioned by month.
CREATE TABLE IF NOT EXISTS tenants (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  email TEXT NOT NULL UNIQUE,
  display_name TEXT NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('admin','operator','installer','viewer')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sites (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  name TEXT NOT NULL,
  address TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS gateways (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  site_id TEXT NOT NULL REFERENCES sites(id),
  serial TEXT NOT NULL UNIQUE,
  cert_fingerprint TEXT,
  status TEXT NOT NULL DEFAULT 'pending', -- pending|active|revoked
  last_seen_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS devices (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  gateway_id TEXT NOT NULL REFERENCES gateways(id),
  profile TEXT NOT NULL,      -- e.g. modbus-energy-meter, door-contact
  name TEXT NOT NULL,
  config JSONB NOT NULL DEFAULT '{}', -- port, baud, register map, poll interval
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS points (
  id TEXT PRIMARY KEY,        -- e.g. 'kwh'
  device_id TEXT NOT NULL REFERENCES devices(id),
  unit TEXT NOT NULL,
  min_value DOUBLE PRECISION,
  max_value DOUBLE PRECISION
);

CREATE TABLE IF NOT EXISTS telemetry (
  event_id TEXT NOT NULL,
  tenant_id TEXT NOT NULL,
  site_id TEXT,
  gateway_id TEXT NOT NULL,
  device_id TEXT NOT NULL,
  point_id TEXT NOT NULL,
  observed_at TIMESTAMPTZ NOT NULL,
  received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  value DOUBLE PRECISION NOT NULL,
  unit TEXT NOT NULL,
  quality TEXT NOT NULL DEFAULT 'measured', -- measured|estimated|missing|stale
  schema_version INT NOT NULL,
  PRIMARY KEY (event_id, observed_at)       -- dedupe by event_id
) PARTITION BY RANGE (observed_at);

CREATE TABLE IF NOT EXISTS telemetry_default PARTITION OF telemetry DEFAULT;

CREATE TABLE IF NOT EXISTS commands (
  request_id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  gateway_id TEXT NOT NULL,
  device_id TEXT NOT NULL,
  action TEXT NOT NULL,
  parameters JSONB NOT NULL DEFAULT '{}',
  status TEXT NOT NULL DEFAULT 'pending_approval', -- pending_approval|approved|sent|acked|failed|expired|rejected
  requested_by TEXT NOT NULL REFERENCES users(id),
  approved_by TEXT REFERENCES users(id),
  issued_at TIMESTAMPTZ,
  expires_at TIMESTAMPTZ,
  policy_version TEXT NOT NULL DEFAULT 'v1',
  outcome JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS rules (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  name TEXT NOT NULL,
  definition JSONB NOT NULL,  -- trigger/condition/action graph from flow builder
  version INT NOT NULL DEFAULT 1,
  enabled BOOLEAN NOT NULL DEFAULT false,
  created_by TEXT NOT NULL REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS alerts (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  rule_id TEXT REFERENCES rules(id),
  severity TEXT NOT NULL CHECK (severity IN ('info','warning','critical')),
  message TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'open', -- open|acknowledged|resolved
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS dashboards (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  name TEXT NOT NULL,
  layout JSONB NOT NULL DEFAULT '{}', -- report/dashboard builder layout
  created_by TEXT NOT NULL REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS audit_log (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  actor TEXT NOT NULL,
  action TEXT NOT NULL,
  target TEXT,
  detail JSONB,
  at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Append-only: revoke UPDATE/DELETE at the role level in production.

CREATE INDEX IF NOT EXISTS telemetry_device_time ON telemetry (device_id, point_id, observed_at DESC);
CREATE INDEX IF NOT EXISTS alerts_tenant_status ON alerts (tenant_id, status);
CREATE INDEX IF NOT EXISTS audit_tenant_time ON audit_log (tenant_id, at DESC);
