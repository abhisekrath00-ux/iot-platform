-- Alert assignment: who is looking after an open alert. Idempotent: migrations re-run on every API start.
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS assigned_to TEXT REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS assigned_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS alerts_assigned_idx ON alerts(tenant_id, assigned_to) WHERE assigned_to IS NOT NULL;
