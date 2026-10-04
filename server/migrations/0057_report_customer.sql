-- A report may be shared with one customer (and its sub-customers). Scoped users see and download only those.
ALTER TABLE reports ADD COLUMN IF NOT EXISTS customer_id TEXT REFERENCES customers(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS reports_customer ON reports (tenant_id, customer_id);
