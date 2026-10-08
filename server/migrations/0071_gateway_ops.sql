CREATE TABLE IF NOT EXISTS gateway_ops (
  id           TEXT PRIMARY KEY,
  tenant_id    TEXT NOT NULL,
  gateway_id   TEXT NOT NULL,
  kind         TEXT NOT NULL CHECK (kind IN ('logs','restart')),
  status       TEXT NOT NULL CHECK (status IN ('pending_approval','requested','done','failed','expired','cancelled')),
  params       JSONB NOT NULL DEFAULT '{}',
  requested_by TEXT NOT NULL,
  approved_by  TEXT,
  result       JSONB,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS gateway_ops_gw ON gateway_ops(tenant_id, gateway_id, created_at DESC);
