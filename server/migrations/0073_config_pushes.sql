CREATE TABLE IF NOT EXISTS gateway_config_pushes (
  id           TEXT PRIMARY KEY,
  tenant_id    TEXT NOT NULL,
  gateway_id   TEXT NOT NULL,
  version      INT  NOT NULL,
  devices_yaml TEXT NOT NULL,
  sha256       TEXT NOT NULL,
  status       TEXT NOT NULL CHECK (status IN ('sent','applied','confirmed','rolled_back','rejected','failed','expired')),
  detail       TEXT NOT NULL DEFAULT '',
  requested_by TEXT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (gateway_id, version)
);
CREATE INDEX IF NOT EXISTS gateway_config_pushes_gw ON gateway_config_pushes(tenant_id, gateway_id, version DESC);
