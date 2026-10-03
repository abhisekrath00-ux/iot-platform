-- Customer hierarchy: a tenant's customers form a tree (sub-customers). Devices belong to at most one
-- customer. A user can be scoped to one customer subtree; scoped users see only devices in it.
CREATE TABLE IF NOT EXISTS customers (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  parent_id TEXT REFERENCES customers(id),
  name TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, parent_id, name)
);
CREATE INDEX IF NOT EXISTS customers_tenant ON customers (tenant_id, parent_id);
ALTER TABLE devices ADD COLUMN IF NOT EXISTS customer_id TEXT REFERENCES customers(id);
CREATE INDEX IF NOT EXISTS devices_customer ON devices (tenant_id, customer_id);
CREATE TABLE IF NOT EXISTS user_customer_scope (
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  user_id TEXT NOT NULL,
  customer_id TEXT NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
  PRIMARY KEY (tenant_id, user_id)
);
