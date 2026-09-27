-- Flow versioning: drafts, publish, rollback. The runtime engine executes
-- only the published version; edits land as drafts until published.
CREATE TABLE IF NOT EXISTS flow_versions (
  id TEXT PRIMARY KEY,
  flow_id TEXT NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  version INT NOT NULL,
  definition JSONB NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('draft','published','archived')),
  created_by TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  published_at TIMESTAMPTZ,
  UNIQUE(flow_id, version)
);
ALTER TABLE flows ADD COLUMN IF NOT EXISTS published_version_id TEXT REFERENCES flow_versions(id);
CREATE INDEX IF NOT EXISTS flow_versions_flow ON flow_versions(flow_id, version DESC);

-- Backfill: every pre-versioning flow becomes v1 published (idempotent).
INSERT INTO flow_versions (id, flow_id, tenant_id, version, definition, status, created_by, published_at)
SELECT 'fv-' || id, id, tenant_id, 1, definition, 'published', created_by, now() FROM flows
ON CONFLICT (id) DO NOTHING;
UPDATE flows f SET published_version_id = 'fv-' || f.id
WHERE published_version_id IS NULL AND EXISTS (SELECT 1 FROM flow_versions v WHERE v.id = 'fv-' || f.id);
