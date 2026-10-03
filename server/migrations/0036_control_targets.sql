-- Control target registry: the only things a flow control node may ask to change. Idempotent (re-run on every API start).
CREATE TABLE IF NOT EXISTS control_targets (
  id             TEXT PRIMARY KEY,
  tenant_id      TEXT NOT NULL REFERENCES tenants(id),
  name           TEXT NOT NULL,
  kind           TEXT NOT NULL CHECK (kind IN ('modbus_write','alarm_output')),
  gateway_id     TEXT NOT NULL,
  device_id      TEXT NOT NULL,
  point_id       TEXT NOT NULL,
  min_value      DOUBLE PRECISION,
  max_value      DOUBLE PRECISION,
  allowed_values DOUBLE PRECISION[],
  max_per_hour   INTEGER NOT NULL DEFAULT 6 CHECK (max_per_hour BETWEEN 1 AND 60),
  approval_mode  TEXT NOT NULL DEFAULT 'approval' CHECK (approval_mode IN ('approval','automatic')),
  enabled        BOOLEAN NOT NULL DEFAULT false,
  created_by     TEXT NOT NULL REFERENCES users(id),
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, name),
  CHECK (approval_mode = 'approval' OR kind = 'alarm_output'),
  CHECK (allowed_values IS NOT NULL OR (min_value IS NOT NULL AND max_value IS NOT NULL AND min_value <= max_value))
);
CREATE INDEX IF NOT EXISTS control_targets_tenant ON control_targets(tenant_id);
