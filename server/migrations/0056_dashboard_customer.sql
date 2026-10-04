-- A dashboard may be shared with one customer (and its sub-customers). Scoped users see only those.
ALTER TABLE dashboards ADD COLUMN IF NOT EXISTS customer_id TEXT REFERENCES customers(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS dashboards_customer ON dashboards (tenant_id, customer_id);
