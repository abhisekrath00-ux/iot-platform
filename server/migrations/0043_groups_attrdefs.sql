-- Device groups and typed attribute definitions. Idempotent: migrations run on every API start.
CREATE TABLE IF NOT EXISTS device_groups (
  id          TEXT PRIMARY KEY,
  tenant_id   TEXT NOT NULL REFERENCES tenants(id),
  name        TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  created_by  TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, name)
);
CREATE TABLE IF NOT EXISTS device_group_members (
  group_id  TEXT NOT NULL REFERENCES device_groups(id) ON DELETE CASCADE,
  tenant_id TEXT NOT NULL,
  device_id TEXT NOT NULL,
  PRIMARY KEY (group_id, device_id)
);
CREATE INDEX IF NOT EXISTS device_group_members_dev ON device_group_members(tenant_id, device_id);

CREATE TABLE IF NOT EXISTS attribute_defs (
  tenant_id   TEXT NOT NULL REFERENCES tenants(id),
  key         TEXT NOT NULL,
  type        TEXT NOT NULL CHECK (type IN ('string','number','boolean','enum')),
  enum_values TEXT[] NOT NULL DEFAULT '{}',
  unit        TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  required    BOOLEAN NOT NULL DEFAULT false,
  PRIMARY KEY (tenant_id, key)
);
